#!/usr/bin/env python3
"""Offline wrapper integration with a real operator and an ephemeral test key.

The adapter replaces only GitHub origins with the operator's local-fixture
mode; no live key, service or authenticated origin is used.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
from unittest import mock
import zipfile

here = Path(__file__).parent
spec = importlib.util.spec_from_file_location("fake_tests", here / "test-prepare-initial-update-feed-078.py")
fakes = importlib.util.module_from_spec(spec); spec.loader.exec_module(fakes)
prepare = fakes.prepare
real_run = subprocess.run


def call(exe, *args):
    result = real_run([str(exe), *map(str,args)], capture_output=True, text=True)
    if result.returncode: raise AssertionError("synthetic operator command failed: " + args[0] + "\n" + result.stderr)
    return result.stdout


def packages(root, version, revision):
    root.mkdir()
    artifacts = []
    for platform in ("windows-amd64", "linux-amd64"):
        name = f"gateway-client-v{version}-{platform}.zip"
        prefix = name[:-4] + "/"
        client, helper = (("GatewayClient.exe", "GatewayUpdateHelper.exe") if platform.startswith("windows") else ("gateway-client", "gateway-update-helper"))
        entries = {client: "fixture", helper: "fixture", "SOURCE-REVISION.txt": f"Repository: https://github.com/Blockamoto/gateway\nCommit: {revision}\nVersion: {version}\n", "COMPATIBILITY.json": "{}", "README.txt": "fixture"}
        if platform.startswith("windows"):
            entries.update({"GatewayOnDemand.exe":"fixture", "GatewayNativeHost.exe":"fixture", "browser-companion/manifest.json":"{}", "browser-companion/IDENTITY.txt":"fixture"})
        with zipfile.ZipFile(root/name, "w") as archive:
            for entry, data in entries.items(): archive.writestr(prefix+entry,data)
        raw=(root/name).read_bytes()
        artifacts.append({"file":name,"bytes":len(raw),"sha256":hashlib.sha256(raw).hexdigest()})
    (root/"artifact-manifest.json").write_text(json.dumps({"version":version,"source_revision":revision,"verification":"zip_crc_and_all_file_hashes_pass","artifacts":artifacts}))
    return artifacts


def header_fixture(path):
    raw=bytes(80)
    tip=hashlib.sha256(hashlib.sha256(raw).digest()).digest()[::-1].hex()
    meta={"schema":1,"network":"mainnet","count":1,"sha256":hashlib.sha256(raw).hexdigest(),"tip_hash":tip,"created_at":"2026-01-01T00:00:00Z"}
    with zipfile.ZipFile(path,"w") as archive:
        archive.writestr("headers-mainnet.bin",raw)
        archive.writestr("headers-mainnet.json",json.dumps(meta))


def main():
    parser=argparse.ArgumentParser();parser.add_argument("--publisher",required=True,type=Path);args=parser.parse_args()
    publisher=args.publisher.resolve()
    with tempfile.TemporaryDirectory(prefix="gateway-signing-real-fixture-") as temp:
        root=Path(temp)
        key,public=root/"private.json",root/"public.json"
        call(publisher,"keygen","-private",key,"-public",public)
        trust=json.loads(public.read_text()); private=key.read_text()
        olddir=root/"old-packages";newdir=root/"new-packages"
        packages(olddir,"0.7.7",prepare.PREVIOUS_REVISION)
        newrows=packages(newdir,"0.7.8","1"*40)
        headers=root/"headers.zip";header_fixture(headers)
        oldfeed=root/"old-feed"
        call(publisher,"publish","-version","0.7.7","-approve-release","0.7.7","-sequence","10","-validity","720h","-private",key,"-out",oldfeed,"-local-fixture",olddir)
        call(publisher,"publish-headers","-version","0.7.7","-approve-release","0.7.7","-sequence","7","-validity","720h","-private",key,"-out",oldfeed,"-local-fixture",headers)
        oldzip=root/"old.zip";call(publisher,"export","-dir",oldfeed,"-public",public,"-out",oldzip)
        oldinspect=json.loads(call(publisher,"restore","-bundle",oldzip,"-out",root/"old-verified","-version","0.7.7","-repository",prepare.REPOSITORY,"-public",public))
        (root/"scenario").mkdir()
        s=fakes.Scenario(root/"scenario");s.publisher=publisher
        s.env["GATEWAY_UPDATE_SIGNING_KEY_JSON"]=private
        s.raw={row["file"]:(newdir/row["file"]).read_bytes() for row in newrows}
        for name in prepare.FILES - set(s.raw): s.raw[name]=(headers.read_bytes() if name.startswith("gateway-headers") else ("fixture "+name).encode())
        s.pins={name:{"bytes":len(raw),"sha256":fakes.sha(raw)} for name,raw in s.raw.items()}
        s.manifest={"version":"0.7.8","source_revision":"1"*40,"artifacts":[{"file":name,**s.pins[name]} for name in sorted(s.raw)]}
        s.manifest_raw=json.dumps(s.manifest).encode()
        s.assets=[{"id":i+10,"name":name,"size":len(raw),"digest":"sha256:"+fakes.sha(raw)} for i,(name,raw) in enumerate(s.raw.items())]
        s.manifest_asset={"id":90,"name":"artifact-manifest.json","size":len(s.manifest_raw),"digest":"sha256:"+fakes.sha(s.manifest_raw)}
        s.previous_raw=oldzip.read_bytes();s.previous_asset={"id":91,"name":"gateway-update-feed-v0.7.7.zip","size":len(s.previous_raw),"digest":"sha256:"+fakes.sha(s.previous_raw)}
        with zipfile.ZipFile(oldzip) as archive: s.live={"/manifest.json":archive.read("manifest.json"),"/headers/manifest.json":archive.read("headers-manifest.json")}
        s.headers=oldinspect["headers"]
        (s.root/"assets/bootstrap/update-public-key.json").write_text(json.dumps(trust))
        (s.root/"assets/bootstrap/update-channels.json").write_text(json.dumps({"channels":[{"id":"stable","publisher_url":prepare.PUBLISHER_URL,"trusted_key":trust}]}))
        s.overrides["KEY"]=trust
        # Route only the two origin-fetching commands through the CLI's explicit
        # local fixture mode. All keygen, signatures, check, export and restore
        # use the real operator and synthetic key. Strip its fixture banner so
        # the production run_tool still parses/validates the actual CLI output.
        def local_run(cmd, **kwargs):
            cmd=list(cmd);op=cmd[1]
            if op=="publish": cmd += ["-local-fixture",str(newdir)]
            if op=="publish-headers": cmd += ["-local-fixture",str(headers)]
            result=real_run(cmd,**kwargs)
            banner=b"Preparing a LOCAL FIXTURE feed; these packages have no authenticated GitHub origin.\n"
            if op=="publish" and result.stdout.startswith(banner): result.stdout=result.stdout[len(banner):]
            return result
        # run_execute normally replaces run_tool with a fake. Override just that
        # mock with the actual wrapper while intercepting its subprocess origin.
        actual_tool=prepare.run_tool
        s.tool=actual_tool
        with mock.patch.object(prepare.subprocess,"run",side_effect=local_run): receipt=s.run_execute()
        if receipt["status"]!="PREPARED_NOT_PROMOTED" or not receipt["private_temporary_file_removed"]: raise AssertionError("unsafe receipt")
        candidate=s.out/"gateway-update-feed-v0.7.8.zip"
        verified=json.loads(call(publisher,"restore","-bundle",candidate,"-out",root/"final-verified","-version","0.7.8","-repository",prepare.REPOSITORY,"-public",public))
        if verified["application"]["sequence"]!=11 or verified["headers"]["sequence"]!=8: raise AssertionError("sequence mismatch")
        other_private,other_public=root/"other-private.json",root/"other-public.json"
        call(publisher,"keygen","-private",other_private,"-public",other_public)
        wrong_key=real_run([str(publisher),"restore","-bundle",str(candidate),"-out",str(root/"wrong-key"),"-version","0.7.8","-repository",prepare.REPOSITORY,"-public",str(other_public)],capture_output=True)
        if wrong_key.returncode == 0: raise AssertionError("wrong trust key accepted")
        tampered=root/"tampered.zip"
        with zipfile.ZipFile(candidate) as src, zipfile.ZipFile(tampered,"w") as dst:
            for name in src.namelist():
                data=src.read(name)
                if name=="manifest.json":
                    modified=bytearray(data);modified[len(modified)//2] ^= 1;data=bytes(modified)
                dst.writestr(name,data)
        bad=real_run([str(publisher),"restore","-bundle",str(tampered),"-out",str(root/"tampered-restore"),"-version","0.7.8","-repository",prepare.REPOSITORY,"-public",str(public)],capture_output=True)
        if bad.returncode == 0: raise AssertionError("tampered candidate accepted")
        if private in json.dumps(receipt) or private.encode() in candidate.read_bytes(): raise AssertionError("synthetic key leaked")
        print("PASS: real operator signed, exported and restored an offline synthetic-key candidate; no network or production keys")

if __name__ == "__main__": main()
