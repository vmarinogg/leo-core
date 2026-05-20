// Sample TypeScript file for Cartographer AST extraction tests.

export class DataProcessor {
  constructor(private config: Config) {}

  process(record: unknown): unknown {
    return record;
  }
}

export interface Config {
  path: string;
}

// Bare (non-exported) interface.
interface RawRecord {
  id: number;
  data: unknown;
}

export function loadConfig(path: string): Config {
  return { path };
}

export const formatValue = (val: unknown): string => String(val);

const counter = 0;
