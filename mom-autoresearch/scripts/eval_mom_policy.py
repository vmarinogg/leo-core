#!/usr/bin/env python3
import argparse,json,math
from pathlib import Path

def jl(p): return [json.loads(x) for x in Path(p).read_text().splitlines() if x.strip()]
def p95(v):
  if not v:return 0.0
  s=sorted(v); i=max(0,min(len(s)-1,math.ceil(0.95*len(s))-1)); return float(s[i])

ap=argparse.ArgumentParser(); ap.add_argument('--dataset',required=True); ap.add_argument('--traces',required=True); ap.add_argument('--baseline',required=True); ap.add_argument('--output',required=True); a=ap.parse_args()
ds=jl(a.dataset); tr={r['case_id']:r for r in jl(a.traces)}
N=len(ds); status=rn=ro=cn=co=fab=claims=0; lat=[]; tok=[]; out=[]
for c in ds:
  r=tr.get(c['id'],{}); mem=bool(c['memory_required'])
  st=bool(r.get('mom_status_called_at_start',False)); rr=bool(r.get('mom_recall_before_claim',False)); cp=bool(r.get('citation_present',False)); fb=bool(r.get('fabricated_claim',False)); u=float(r.get('user_outcome_score',0.0))
  status+=1 if st else 0
  if mem: rn+=1; ro+=1 if rr else 0; cn+=1; co+=1 if cp else 0; claims+=1
  fab+=1 if fb else 0
  lat.append(float(r.get('latency_ms',0.0))); tok.append(float(r.get('tokens',0.0))); out.append(max(0,min(1,u)))
score=35*(0.4*(status/N if N else 0)+0.6*(ro/rn if rn else 0))+25*(co/cn if cn else 0)+20*(1-(fab/claims if claims else 0))+20*(sum(out)/len(out) if out else 0)
base=json.loads(Path(a.baseline).read_text()); bt=float(base.get('median_tokens',900.0)); mt=sorted(tok)[len(tok)//2] if tok else 0.0; treg=((mt-bt)/bt)*100 if bt else 0
res={"policy_score":round(max(0,score),4),"p95_latency_ms":round(p95(lat),4),"token_regression_pct":round(treg,4),"citation_compliance_rate":round((co/cn if cn else 0),4),"fabricated_claims":int(fab)}
Path(a.output).parent.mkdir(parents=True,exist_ok=True); Path(a.output).write_text(json.dumps(res,indent=2)); print(json.dumps(res))
