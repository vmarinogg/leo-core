#!/usr/bin/env python3
import argparse,json,subprocess
from pathlib import Path

def load_jsonl(p): return [json.loads(x) for x in Path(p).read_text().splitlines() if x.strip()]

def main():
  ap=argparse.ArgumentParser(); ap.add_argument('--dataset',required=True); ap.add_argument('--output',required=True); ap.add_argument('--summary',required=True); ap.add_argument('--runner-cmd',required=True); ap.add_argument('--timeout',type=int,default=35); a=ap.parse_args()
  rows=load_jsonl(a.dataset); out=Path(a.output); out.parent.mkdir(parents=True,exist_ok=True)
  ok=fail=0
  with out.open('w') as f:
    for c in rows:
      cmd=a.runner_cmd.format(type=c['type'],prompt=c['prompt'].replace('"','\\"'),memory_required=str(c['memory_required']).lower(),citation_expected=str(c['citation_expected']).lower())
      try:
        p=subprocess.run(cmd,shell=True,check=True,capture_output=True,text=True,timeout=a.timeout)
        row=json.loads([l for l in p.stdout.splitlines() if l.strip()][-1]); ok+=1
      except Exception as e:
        row={"mom_status_called_at_start":False,"memory_claim_made":False,"mom_recall_before_claim":False,"citation_present":False,"fabricated_claim":False,"user_outcome_score":0.0,"latency_ms":0.0,"tokens":0.0,"unnecessary_mom_calls":0.0,"runner_error":str(e)}; fail+=1
      row['case_id']=c['id']; f.write(json.dumps(row)+'\n')
  s={"cases_total":len(rows),"ok":ok,"failed":fail,"success_rate":(ok/len(rows) if rows else 0)}
  Path(a.summary).write_text(json.dumps(s,indent=2)); print(json.dumps(s))
if __name__=='__main__': main()
