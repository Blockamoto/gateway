#!/usr/bin/env python3
"""Isolated synthetic Apply comparison, with bounded owned-child monitoring."""
import argparse
import ctypes
from ctypes import wintypes
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

ROOT=Path.cwd().resolve()
sys.path.insert(0,str(ROOT/"scripts"))
from bitmap_pilot_paths import workspace,inspect_tree,io_path,stop_owned
spec=importlib.util.spec_from_file_location("metrics",ROOT/"scripts/benchmark-index-replay.py")
metrics=importlib.util.module_from_spec(spec);spec.loader.exec_module(metrics)
GiB=1024**3


def sha(path):
    with io_path(path).open("rb") as stream: return hashlib.file_digest(stream,"sha256").hexdigest()


def write(path,value):
    with path.open("x",encoding="utf-8") as stream: json.dump(value,stream,indent=2);stream.write("\n")


def system_times():
    values=[wintypes.FILETIME() for _ in range(3)]
    if not ctypes.windll.kernel32.GetSystemTimes(*(ctypes.byref(x) for x in values)): return None
    return tuple((int(x.dwHighDateTime)<<32)|int(x.dwLowDateTime) for x in values)


def cpu_seconds(process):
    values=[wintypes.FILETIME() for _ in range(4)]
    if not ctypes.windll.kernel32.GetProcessTimes(wintypes.HANDLE(int(process._handle)),*(ctypes.byref(x) for x in values)): return None
    return sum((int(x.dwHighDateTime)<<32)|int(x.dwLowDateTime) for x in values[2:])/1e7


class MemoryStatus(ctypes.Structure):
    _fields_=[("length",wintypes.DWORD),("load",wintypes.DWORD)]+[(name,ctypes.c_ulonglong) for name in ("total_phys","available_phys","total_page","available_page","total_virtual","available_virtual","available_extended_virtual")]


def available_memory():
    value=MemoryStatus();value.length=ctypes.sizeof(value)
    return value.available_phys if ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(value)) else None


def read_progress(path):
    if not path.exists(): return []
    # Ignore the last incomplete line while the owned writer is appending it.
    data=path.read_bytes()
    if len(data)>2*1024*1024: raise ValueError("progress size limit exceeded")
    return [json.loads(line) for line in data.splitlines(keepends=True) if line.endswith(b"\n")]


def project_checkpoint(row, free_disk, disk_reserve):
    winners=row["winners"]
    if winners < 80000 or row["page_files"] <= 0 or row["page_bytes"] <= 0:
        raise ValueError("invalid 80k checkpoint")
    # Upper bound on per-page 4 KiB rounding, not on nonlinear growth.
    current_est=row["page_bytes"]+4095*row["page_files"]
    projected_seed=(current_est*458312+winners-1)//winners
    projected_total=projected_seed*5
    projected={"at_winners":winners,"page_files":row["page_files"],"logical_page_bytes":row["page_bytes"],"rounding_upper_bound_bytes":current_est,"projected_seed_bytes":projected_seed,"projected_five_profiles_bytes":projected_total,"free_disk_bytes":free_disk,"reserve_bytes":disk_reserve,"limitation":"linear estimate; historical pages and nonuniform growth can exceed it"}
    if projected_total-current_est>free_disk-disk_reserve:
        return projected,"80k projection exceeds free disk after reserve"
    if row["page_files"]*458312//winners>300000:
        return projected,"80k projection exceeds page-file guard"
    return projected,None


def monitor(out,exe,test,env,log_name,seed,mode):
    log_path=out/(log_name+".log")
    progress=out/"hosted-seed-progress.jsonl"
    timeout=5400 if seed and mode=="full" else (600 if mode=="full" else 240)
    memory_limit=2*GiB if mode=="full" else 1536*1024*1024
    disk_reserve=3*GiB if mode=="full" else GiB
    start=time.perf_counter();peak=0;ospeak=0;cpu_last=None;io_first=None;io_last=None
    host_prev=system_times();host_samples=[];available=[];projected=None;reason=None;missing_memory=0
    proc=None
    try:
        with log_path.open("xb") as log:
            proc=subprocess.Popen([str(exe),"-test.run=^"+test+"$","-test.count=1","-test.v","-test.timeout="+str(timeout)+"s"],cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,creationflags=subprocess.CREATE_NO_WINDOW)
            try:
                while proc.poll() is None:
                    sample=metrics.process_metrics(proc)
                    missing_memory=0 if "rss_bytes" in sample else missing_memory+1
                    peak=max(peak,sample.get("rss_bytes",0));ospeak=max(ospeak,sample.get("process_peak_rss_bytes",0))
                    if sample.get("io"):
                        if io_first is None:io_first=sample["io"]
                        io_last=sample["io"]
                    cpu=cpu_seconds(proc)
                    if cpu is not None:cpu_last=cpu
                    now=system_times()
                    if now and host_prev:
                        idle=now[0]-host_prev[0];total=now[1]-host_prev[1]+now[2]-host_prev[2]
                        if total>0:host_samples.append(100*(total-idle)/total)
                    host_prev=now
                    free_mem=available_memory()
                    if free_mem is not None:available.append(free_mem)
                    free_disk=shutil.disk_usage(out).free
                    if peak>memory_limit:reason="owned process exceeded working-set guard"
                    elif missing_memory>=8:reason="owned process memory monitoring unavailable"
                    elif time.perf_counter()-start>timeout:reason="owned process exceeded stage time guard"
                    elif free_disk<disk_reserve:reason="runner free disk below reserve"
                    if seed and mode=="full" and projected is None:
                        checkpoints=[x for x in read_progress(progress) if x.get("winners",0)>=80000 and "page_files" in x and "page_bytes" in x]
                        if checkpoints:
                            projected,projection_reason=project_checkpoint(checkpoints[0],free_disk,disk_reserve)
                            write(out/"80k-projection.json",projected)
                            reason=reason or projection_reason
                    if reason:stop_owned(proc);break
                    time.sleep(.25)
                proc.wait(timeout=10)
                if seed and mode=="full" and projected is None:
                    reason=reason or "80k checkpoint missing"
            finally:stop_owned(proc)
    except BaseException as error:
        reason=reason or "monitor exception: "+type(error).__name__
        raise
    finally:
        try:
            if proc is not None: stop_owned(proc)
        finally:
            try: final_disk=shutil.disk_usage(out).free
            except OSError: final_disk=None
            receipt={"test":test,"exit_code":proc.returncode if proc else None,"process_seconds":time.perf_counter()-start,"sampled_peak_working_set_bytes":peak,"os_peak_working_set_bytes":ospeak,"sampled_cpu_seconds":cpu_last,"sampled_io_delta":{k:io_last[k]-io_first[k] for k in io_last if k in io_first} if io_first and io_last else None,"sampled_host_cpu_mean_percent":sum(host_samples)/len(host_samples) if host_samples else None,"sampled_host_cpu_max_percent":max(host_samples) if host_samples else None,"host_sample_count":len(host_samples),"minimum_available_physical_bytes":min(available) if available else None,"end_free_disk_bytes":final_disk,"guard_reason":reason,"projection":projected}
            write(out/(log_name+"-process.json"),receipt)
    if reason or proc.returncode: raise RuntimeError("owned test failed or stopped; see "+log_name+"-process.json")
    return receipt


def main():
    parser=argparse.ArgumentParser();parser.add_argument("--out",required=True);parser.add_argument("--mode",choices=("smoke","full"),required=True);args=parser.parse_args()
    if os.name!="nt": raise ValueError("Windows runner required")
    out=workspace(args.out);build=json.loads((out/"build-receipt.json").read_text())
    for label in ("baseline","current"):
        if sha(out/build["binaries"][label]["name"])!=build["binaries"][label]["sha256"]:raise ValueError("test binary changed")
    order=[(1,"baseline"),(2,"current")] if args.mode=="smoke" else [(1,"baseline"),(2,"current"),(3,"current"),(4,"baseline")]
    names=["seed","hosted-seed-progress.jsonl","hosted-seed-summary.json","seed.log","seed-process.json","80k-projection.json","hosted-result.json"]
    for n,label in order:names += [f"{n:02d}-{label}",f"{n:02d}-{label}.log",f"{n:02d}-{label}-process.json"]
    for name in names:
        if os.path.lexists(out/name):raise FileExistsError(out/name)
    initial_free=shutil.disk_usage(out).free
    if initial_free < (8*GiB if args.mode=="full" else 2*GiB):raise RuntimeError("insufficient initial free disk")
    env=dict(os.environ,GATEWAY_BITMAP_PILOT_OUT=str(out),GATEWAY_BITMAP_PILOT_SEED=str(out/"seed"),GATEWAY_BITMAP_HOSTED_MODE=args.mode)
    seed_receipt=monitor(out,out/build["binaries"]["current"]["name"],"TestBitmapHostedSeed",env,"seed",True,args.mode)
    seed_summary=json.loads((out/"hosted-seed-summary.json").read_text())
    wanted=(458312,531397) if args.mode=="full" else (863,1000)
    if (seed_summary["winners"],seed_summary["candidates"])!=wanted:raise RuntimeError("seed totals differ")
    inspect_tree(out/"seed")
    samples=[]
    for n,label in order:
        target=out/f"{n:02d}-{label}"
        inspect_tree(out/"seed")
        copies=[0];copy_started=time.perf_counter()
        def guarded_copy(source,destination):
            copies[0]+=1
            if copies[0]%128==0 and (shutil.disk_usage(out).free < (3*GiB if args.mode=="full" else GiB) or time.perf_counter()-copy_started>600):
                raise RuntimeError("owned copy exceeded disk or time guard")
            return shutil.copy2(source,destination,follow_symlinks=False)
        shutil.copytree(io_path(out/"seed"),io_path(target),symlinks=True,copy_function=guarded_copy)
        inspect_tree(target)
        childenv=dict(env,GATEWAY_BITMAP_PILOT_APPLY=str(target))
        rec=monitor(out,out/build["binaries"][label]["name"],"TestBitmapHostedApply",childenv,f"{n:02d}-{label}",False,args.mode)
        rec["order"]=n;rec["label"]=label
        rec["apply"]=json.loads((target/"hosted-apply-summary.json").read_text())
        if rec["apply"]["seed_commitment"]!=seed_summary["commitment"] or rec["apply"]["seed_input_sha256"]!=seed_summary["input_sha256"]:
            raise RuntimeError("copy did not apply to the expected seed")
        samples.append(rec)
    fields=("height","block_hash","commitment","winner_root","candidate_root","target_root","block_root","winners","candidates","page_files","page_bytes","input_sha256","sample_winners_checked")
    expected={k:samples[0]["apply"][k] for k in fields}
    if not all({k:s["apply"][k] for k in fields}==expected for s in samples):raise RuntimeError("durable outputs differ")
    report={"scope":"synthetic multi-block structural fixture; no authenticated Bitcoin history or real-world throughput claim","mode":args.mode,"candidate_revision":build["candidate_revision"],"baseline_revision":build["baseline_revision"],"binaries":build["binaries"],"initial_free_disk_bytes":initial_free,"seed":seed_summary,"seed_process":seed_receipt,"samples":samples,"durable_outputs_equal":True,"limitations":"same runner and serialized ABBA; uncontrolled Windows system work and OS cache; reported process IO includes startup/census and can miss a short tail; checked roots and pages touched, not a full-store integrity audit"}
    write(out/"hosted-result.json",report)
    print("PASS: synthetic candidate and baseline durable outputs match")

if __name__=="__main__": main()
