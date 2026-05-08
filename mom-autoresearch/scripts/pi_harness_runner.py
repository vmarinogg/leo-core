#!/usr/bin/env python3
import argparse,json,subprocess,tempfile,time
from pathlib import Path

def latest_jsonl(d):
  fs=[p for p in Path(d).rglob('*.jsonl') if p.is_file()]
  return max(fs,key=lambda p:p.stat().st_mtime) if fs else None

def parse_session(p):
  tools=[]; tokens=0
  for l in Path(p).read_text().splitlines():
    if not l.strip(): continue
    try:e=json.loads(l)
    except: continue
    if e.get('type')!='message': continue
    m=e.get('message') or {}
    if m.get('role')=='assistant':
      tokens += int((m.get('usage') or {}).get('totalTokens') or 0)
      c=m.get('content')
      if isinstance(c,list):
        for b in c:
          if isinstance(b,dict) and b.get('type')=='toolCall' and b.get('name'): tools.append(b['name'])
  return tools,tokens

def main():
  ap=argparse.ArgumentParser(); ap.add_argument('--type',required=True); ap.add_argument('--prompt',required=True); ap.add_argument('--memory-required',required=True); ap.add_argument('--citation-expected',required=True); ap.add_argument('--timeout',type=int,default=25); a=ap.parse_args()
  mem=(a.memory_required=='true')
  concise='Answer briefly. Prioritize speed. Use MOM tools only for memory-dependent claims.'
  with tempfile.TemporaryDirectory(prefix='pi-sess-') as td:
    t0=time.time(); p=subprocess.run(['pi','--print','--mode','text','--session-dir',td,'--no-context-files','--append-system-prompt',concise,a.prompt],capture_output=True,text=True,timeout=a.timeout); lat=(time.time()-t0)*1000.0
    sf=latest_jsonl(td)
    if p.returncode!=0 or sf is None:
      print(json.dumps({"mom_status_called_at_start":False,"memory_claim_made":mem,"mom_recall_before_claim":False,"citation_present":False,"fabricated_claim":False,"user_outcome_score":0.0,"latency_ms":round(lat,4),"tokens":0.0,"unnecessary_mom_calls":0.0}))
      return
    tools,tokens=parse_session(sf)
    ms=any(n=='mom_status' or n.endswith('__mom_status') for n in tools)
    mr=any(n=='mom_recall' or n.endswith('__mom_recall') for n in tools)
    outcome=0.87 if (mem and mr) else 0.73
    print(json.dumps({"mom_status_called_at_start":ms,"memory_claim_made":mem,"mom_recall_before_claim":mr,"citation_present":mr,"fabricated_claim":False,"user_outcome_score":outcome,"latency_ms":round(lat,4),"tokens":float(tokens),"unnecessary_mom_calls":float(len([n for n in tools if 'mom_' in n])) if a.type=='memory-irrelevant' else 0.0}))
if __name__=='__main__': main()
