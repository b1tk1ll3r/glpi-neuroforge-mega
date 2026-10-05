#!/usr/bin/env python3
"""Run the read-only retrieval/learning replay benchmark against a live Agent."""
import argparse, base64, json, pathlib, sys, urllib.request, urllib.error

p=argparse.ArgumentParser()
p.add_argument('cases', help='JSON file: {"cases":[...]}')
p.add_argument('--url', default='http://127.0.0.1:8080')
p.add_argument('--user', default='')
p.add_argument('--password', default='')
p.add_argument('--output', default='')
a=p.parse_args()
payload=pathlib.Path(a.cases).read_bytes()
req=urllib.request.Request(a.url.rstrip('/')+'/api/quality/replay', data=payload, method='POST', headers={'Content-Type':'application/json','X-Requested-With':'GLPI-AI-Agent'})
if a.user or a.password:
    token=base64.b64encode(f'{a.user}:{a.password}'.encode()).decode()
    req.add_header('Authorization','Basic '+token)
try:
    with urllib.request.urlopen(req, timeout=300) as r:
        out=r.read()
except urllib.error.HTTPError as e:
    sys.stderr.write(e.read().decode(errors='replace')+'\n')
    raise SystemExit(2)
if a.output:
    pathlib.Path(a.output).write_bytes(out+b'\n')
obj=json.loads(out)
print(json.dumps(obj.get('summary',{}), indent=2, ensure_ascii=False))
