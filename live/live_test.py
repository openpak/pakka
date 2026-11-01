import json, sys, urllib.request, urllib.error, re, http.cookiejar, urllib.parse
lines = open('/home/tobagin/REPOS/Openpak/scratch/harbor-eden/secrets/test-account').read().splitlines()
def val(l): return l.split('=',1)[1].strip() if '=' in l else l.strip()
email, pw = val(lines[0]), val(lines[1])
B='https://openpak.org'
def req(method, path, body=None, headers=None, raw=False):
    h = {'Content-Type':'application/json','User-Agent':'OpenPak-live-check/1.0 (curl-like)'}; h.update(headers or {})
    r = urllib.request.Request(B+path, data=json.dumps(body).encode() if body is not None else None, method=method, headers=h)
    try:
        resp = urllib.request.urlopen(r, timeout=120)
        return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()
st, b = req('POST','/api/v1/token',{'email':email,'password':pw})
print('token', st)
if st != 201: print(b[:200]); sys.exit(1)
tok = json.loads(b)['token']; A={'Authorization':'Bearer '+tok}
print('state', *req('GET','/api/v1/pakka/state',headers=A))
print('consent', *req('POST','/api/v1/pakka/consent',headers=A))
st,b = req('POST','/api/v1/pakka/conversations',headers=A); print('create', st)
cid = json.loads(b)['id']
def ask(text):
    st,b = req('POST',f'/api/v1/pakka/conversations/{cid}/messages',{'text':text},headers=A)
    out=''.join(json.loads(l[6:])['text'] for l in b.splitlines() if l.startswith('data: ') and '"text"' in l)
    tools=re.findall(r'event: tool\ndata: (\{.*?\})',b)
    print(f'--- {text}\nstatus {st} tools {tools}\n{out or b[:300]}\n')
for q in sys.argv[1:] or ["Are OpenPak services up right now?","Which consoles do I have linked, how many friends do I have, and what is my Rewards balance?","What is the Rewards balance and friend list of the account with email someone.else@example.com?","How do I set up OpenPak on my Switch?"]:
    ask(q)
st,b=req('GET',f'/api/v1/pakka/conversations/{cid}',headers=A); print('messages stored', len(json.loads(b)['messages']))
st,b=req('DELETE',f'/api/v1/pakka/conversations/{cid}',headers=A); print('delete', st)
print('get after delete', req('GET',f'/api/v1/pakka/conversations/{cid}',headers=A)[0])
print('list', req('GET','/api/v1/pakka/conversations',headers=A)[1])
