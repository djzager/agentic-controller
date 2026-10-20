#!/usr/bin/env python3
"""Metadata-only passive viewer for the direct-Sandbox shared-runtime demo."""
import argparse
import base64
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
from urllib.error import HTTPError, URLError
from urllib.request import urlopen


def save(path, value):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
        json.dump(value, f, indent=2)
        f.write("\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", default="lightspeed-konveyor-poc")
    parser.add_argument("--run", required=True)
    parser.add_argument("--sandbox-uid", required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--probe", type=Path, required=True)
    args = parser.parse_args()
    if args.out.stat().st_mode & 0o077:
        raise ValueError("private_output_directory_required")
    prefix = ["oc", "--context", args.context, "-n", args.namespace, "--request-timeout=15s"]

    def oc(*cmd):
        result = subprocess.run(prefix + list(cmd), capture_output=True, text=True, timeout=30)
        if result.returncode:
            raise RuntimeError("observer_cluster_read_failed")
        return json.loads(result.stdout)

    def stop(signum, frame):
        raise KeyboardInterrupt()

    signal.signal(signal.SIGTERM, stop)
    deadline = time.monotonic() + 180
    pod = None
    while time.monotonic() < deadline:
        pods = oc("get", "pods", "-o", "json")["items"]
        owned = [p for p in pods if any(o["uid"] == args.sandbox_uid
                 for o in p["metadata"].get("ownerReferences", []))]
        if len(owned) > 1:
            raise RuntimeError("ambiguous_sandbox_pod")
        if (owned and owned[0].get("status", {}).get("phase") == "Running"
                and any(c.get("type") == "Ready" and c.get("status") == "True"
                        for c in owned[0].get("status", {}).get("conditions", []))):
            pod = owned[0]
            break
        time.sleep(0.5)
    if pod is None:
        raise RuntimeError("running_owned_pod_missing")
    key = base64.b64decode(oc("get", "secret", args.run + "-acp", "-o", "json")["data"]["token"]).decode()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    probe = None
    with (args.out / "acp-forward.log").open("w") as log:
        forward = subprocess.Popen(prefix + ["port-forward", "pod/" + pod["metadata"]["name"],
            f"{port}:4000", "--address=127.0.0.1"], stdout=log, stderr=log)
        try:
            for _ in range(450):
                if forward.poll() is not None:
                    raise RuntimeError("passive_port_forward_exited")
                try:
                    with urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1) as response:
                        if response.status == 200:
                            break
                except (URLError, TimeoutError):
                    time.sleep(0.2)
            else:
                raise RuntimeError("passive_tee_not_ready")
            try:
                urlopen(f"http://127.0.0.1:{port}/acp", timeout=3)
                raise RuntimeError("unauthenticated_acp_was_not_rejected")
            except HTTPError as error:
                if error.code != 401:
                    raise RuntimeError("unexpected_unauthenticated_status") from None
            probe = subprocess.Popen([str(args.probe)], stdin=subprocess.PIPE,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            stdout, _stderr = probe.communicate(json.dumps({"URL": f"ws://127.0.0.1:{port}/acp",
                "Key": key, "CWD": "/workspace"}), timeout=650)
            if probe.returncode:
                raise RuntimeError("passive_probe_process_failed")
            report = json.loads(stdout)
            report.update(run=args.run, sandboxUID=args.sandbox_uid, pod=pod["metadata"]["name"],
                podUID=pod["metadata"]["uid"], unauthenticatedHTTP=401,
                transport="localhost oc port-forward Pod:4000", interaction="watch_only")
            save(args.out / "acp-observation.json", report)
            print(json.dumps(report), flush=True)
            if not report.get("observedRunStream") or not report.get("newToolAfterLoad") or report.get("error"):
                raise RuntimeError("passive_reconnect_assertion_failed")
        finally:
            if probe is not None and probe.poll() is None:
                probe.terminate()
                probe.wait(timeout=10)
            forward.terminate()
            forward.wait(timeout=10)


if __name__ == "__main__":
    main()
