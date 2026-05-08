/**
 * mom-tools: expose MOM's MCP tools as native Pi tools.
 *
 * Spawns `mom serve mcp` as a stdio child process, speaks MCP JSON-RPC, and
 * registers each MOM tool with `pi.registerTool()` so the LLM can call them
 * directly (e.g. `mom_recall`, `mom_status`, `search_memories`, ...) without
 * going through the generic `mcp` gateway.
 */

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { Type, type TSchema } from "typebox";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

interface McpToolDef {
	name: string;
	description?: string;
	inputSchema?: any;
}

interface RpcPending {
	resolve: (value: any) => void;
	reject: (err: Error) => void;
}

class StdioMcpClient {
	private child: ChildProcessWithoutNullStreams;
	private buf = "";
	private nextId = 1;
	private pending = new Map<number, RpcPending>();
	private ready: Promise<void>;

	constructor(command: string, args: string[], env: Record<string, string>) {
		this.child = spawn(command, args, {
			env: { ...process.env, ...env },
			stdio: ["pipe", "pipe", "pipe"],
		});
		this.child.stdout.setEncoding("utf8");
		this.child.stdout.on("data", (chunk: string) => this.onStdout(chunk));
		this.child.stderr.on("data", () => {
			// Swallow stderr; MOM logs there. Could surface via ctx.ui if desired.
		});
		this.child.on("exit", () => {
			for (const p of this.pending.values()) {
				p.reject(new Error("MOM MCP server exited"));
			}
			this.pending.clear();
		});

		this.ready = this.handshake();
	}

	private onStdout(chunk: string) {
		this.buf += chunk;
		let idx: number;
		while ((idx = this.buf.indexOf("\n")) >= 0) {
			const line = this.buf.slice(0, idx).trim();
			this.buf = this.buf.slice(idx + 1);
			if (!line) continue;
			let msg: any;
			try {
				msg = JSON.parse(line);
			} catch {
				continue;
			}
			if (typeof msg.id === "number" && this.pending.has(msg.id)) {
				const p = this.pending.get(msg.id)!;
				this.pending.delete(msg.id);
				if (msg.error) p.reject(new Error(msg.error.message ?? "MCP error"));
				else p.resolve(msg.result);
			}
		}
	}

	private send(obj: any) {
		this.child.stdin.write(JSON.stringify(obj) + "\n");
	}

	private request(method: string, params?: any): Promise<any> {
		const id = this.nextId++;
		return new Promise((resolve, reject) => {
			this.pending.set(id, { resolve, reject });
			this.send({ jsonrpc: "2.0", id, method, params });
		});
	}

	private notify(method: string, params?: any) {
		this.send({ jsonrpc: "2.0", method, params });
	}

	private async handshake() {
		await this.request("initialize", {
			protocolVersion: "2024-11-05",
			capabilities: {},
			clientInfo: { name: "pi-mom-tools", version: "0.1.0" },
		});
		this.notify("notifications/initialized");
	}

	async listTools(): Promise<McpToolDef[]> {
		await this.ready;
		const result = await this.request("tools/list");
		return result?.tools ?? [];
	}

	async callTool(name: string, args: any): Promise<any> {
		await this.ready;
		return this.request("tools/call", { name, arguments: args ?? {} });
	}

	dispose() {
		try {
			this.child.kill();
		} catch {
			// ignore
		}
	}
}

/** Convert a JSON Schema (object) to a TypeBox schema. Best-effort. */
function jsonSchemaToTypeBox(schema: any): TSchema {
	if (!schema || typeof schema !== "object") return Type.Any();
	const t = schema.type;
	const desc = schema.description ? { description: schema.description } : {};

	if (Array.isArray(t)) {
		// Union of primitives
		return Type.Union(t.map((x: string) => jsonSchemaToTypeBox({ ...schema, type: x })), desc);
	}

	switch (t) {
		case "string": {
			if (Array.isArray(schema.enum)) return Type.Union(schema.enum.map((v: string) => Type.Literal(v)), desc);
			return Type.String(desc);
		}
		case "integer":
		case "number":
			return Type.Number(desc);
		case "boolean":
			return Type.Boolean(desc);
		case "array":
			return Type.Array(schema.items ? jsonSchemaToTypeBox(schema.items) : Type.Any(), desc);
		case "object": {
			const props: Record<string, TSchema> = {};
			const required: string[] = Array.isArray(schema.required) ? schema.required : [];
			const sProps = schema.properties ?? {};
			for (const [key, sub] of Object.entries(sProps)) {
				const inner = jsonSchemaToTypeBox(sub);
				props[key] = required.includes(key) ? inner : Type.Optional(inner);
			}
			return Type.Object(props, desc);
		}
		default:
			return Type.Any();
	}
}

/** Read MCP server config from .mcp.json (project) or fall back to a default. */
function loadMomServerConfig(cwd: string): { command: string; args: string[]; env: Record<string, string> } | null {
	const candidates = [path.join(cwd, ".mcp.json"), path.join(process.env.HOME ?? "", ".pi", "agent", ".mcp.json")];
	for (const file of candidates) {
		try {
			if (!fs.existsSync(file)) continue;
			const json = JSON.parse(fs.readFileSync(file, "utf8"));
			const srv = json?.mcpServers?.mom;
			if (srv?.command) {
				return {
					command: srv.command,
					args: Array.isArray(srv.args) ? srv.args : [],
					env: srv.env ?? {},
				};
			}
		} catch {
			// skip
		}
	}
	// Sensible default
	return { command: "mom", args: ["serve", "mcp"], env: {} };
}

function extractText(mcpResult: any): string {
	if (!mcpResult) return "";
	const content = mcpResult.content;
	if (Array.isArray(content)) {
		return content
			.map((c: any) => {
				if (c?.type === "text") return c.text ?? "";
				if (c?.type === "json") return JSON.stringify(c.json ?? c.data ?? c, null, 2);
				return JSON.stringify(c);
			})
			.join("\n");
	}
	return typeof mcpResult === "string" ? mcpResult : JSON.stringify(mcpResult, null, 2);
}

export default async function (pi: ExtensionAPI) {
	const cfg = loadMomServerConfig(process.cwd());
	if (!cfg) return;

	const client = new StdioMcpClient(cfg.command, cfg.args, cfg.env);

	let tools: McpToolDef[] = [];
	try {
		tools = await client.listTools();
	} catch (err) {
		console.error("[mom-tools] failed to list MOM MCP tools:", (err as Error).message);
		client.dispose();
		return;
	}

	for (const tool of tools) {
		// Prefix with `mom_` so they live in a clear namespace and don't collide.
		const name = tool.name.startsWith("mom_") ? tool.name : `mom_${tool.name}`;
		const parameters = jsonSchemaToTypeBox(tool.inputSchema ?? { type: "object", properties: {} });

		pi.registerTool({
			name,
			label: name,
			description: tool.description ?? `MOM tool: ${tool.name}`,
			parameters: parameters as any,
			async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
				try {
					const args = params ?? {};
					if (tool.name === "mom_record") {
						// Pi exposes the real runtime session ID to extensions. Always stamp it
						// here so the model cannot accidentally invent a fake session_id.
						args.session_id = ctx.sessionManager.getSessionId();
					}
					const result = await client.callTool(tool.name, args);
					const text = extractText(result);
					return {
						content: [{ type: "text", text: text || "(empty result)" }],
						details: result,
						isError: !!result?.isError,
					};
				} catch (err) {
					return {
						content: [{ type: "text", text: `Error calling ${tool.name}: ${(err as Error).message}` }],
						details: { error: (err as Error).message },
						isError: true,
					};
				}
			},
		});
	}

	pi.on("session_shutdown", async () => {
		client.dispose();
	});

	pi.registerCommand("mom-tools", {
		description: "List MOM tools registered as native Pi tools",
		handler: async (_args, ctx) => {
			ctx.ui.notify(
				`MOM tools (${tools.length}): ${tools.map((t) => (t.name.startsWith("mom_") ? t.name : `mom_${t.name}`)).join(", ")}`,
				"info",
			);
		},
	});
}
