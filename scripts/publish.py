from pathlib import Path
import os, json, re, hashlib, zipfile, subprocess, sys
root = Path(__file__).resolve().parents[1]
os.chdir(root)
release = json.loads(Path('release.json').read_text(encoding='utf-8'))
version, notes = release['version'], release['notes']
def parts(v):
    a = tuple(int(n) for n in v.split('.'))
    if len(a)!=3 or any(n < 0 or n > 99999 for n in a) or '.'.join(map(str,a)) != v: raise ValueError('Invalid version')
    return a
assert isinstance(notes,list) and all(isinstance(n,str) for n in notes) and notes
parts(version)
source=Path('game/update.go').read_text(encoding='utf-8')
assert re.search(r'const appVersion = "([^\"]+)"',source).group(1)==version, 'Version mismatch in game/update.go'
folder=Path('updates'); folder.mkdir(exist_ok=True)
latest=folder/'latest.json'
if latest.exists() and not (len(sys.argv)>1 and sys.argv[1]=='--verify-build'):
    old=json.loads(latest.read_text(encoding='utf-8'))['version']
    if parts(version)==parts(old):
        print('Version already published'); sys.exit(0)
    if parts(version)<parts(old): raise ValueError('Refusing downgrade')
verify=len(sys.argv)>1 and sys.argv[1]=='--verify-build'
if verify:
    folder=Path('build-check'); folder.mkdir(exist_ok=True); latest=folder/'latest.json'
env=dict(os.environ, GOOS='windows', GOARCH='amd64', CGO_ENABLED='0')
subprocess.run(['go','build','-trimpath','-ldflags=-s -w -H=windowsgui','-o','setup/QuietWorld.exe','./game'],env=env,check=True)
subprocess.run(['go','test','./game'],check=True)
subprocess.run(['node','tests/simulation.js'],check=True)
exe=Path('setup/QuietWorld.exe').read_bytes()
sha=lambda b:hashlib.sha256(b).hexdigest()
manifest=json.dumps(dict(product='QuietWorld',version=version,sha256=sha(exe),notes=notes),ensure_ascii=False).encode('utf-8')
if len(manifest)>4096: raise ValueError('Notes exceed manifest limit')
package=folder/f'QuietWorld_{version}.qwupdate'
with zipfile.ZipFile(package,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as z:
    z.writestr('manifest.json',manifest); z.writestr('QuietWorld.exe',exe)
if package.stat().st_size>64*1024*1024: raise ValueError('Package too large')
subprocess.run(['go','build','-trimpath','-ldflags=-s -w -H=windowsgui','-o',str(folder/'QuietWorld_Setup.exe'),'./setup'],env=env,check=True)
latest.write_text(json.dumps(dict(product='QuietWorld',version=version,package=package.name,sha256=sha(package.read_bytes()),notes=notes),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print('Built '+version+' in '+str(folder))
