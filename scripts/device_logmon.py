#!/usr/bin/env python3
"""
ClaudWorker device log monitor — single-shot serial capture + bug analysis.

Called by the Go dashboard (master/devices_logmon.go) once per device per check
cycle. Opens the serial port (which resets the board on the WCH auto-reset bridge
= a fresh boot each check = the "interaction"), captures for a window, analyses the
log for REAL fault signatures, and prints ONE JSON object to stdout:

  {ok, port, name, booted, setup_complete, littlefs_ok, crypto_ok, provisioning,
   heap_min, heap_max, heap_decline, severity, findings:[{sig,count,sample}],
   log_tail, captured_at, bytes}

Severity: "ok" | "warn" | "bug". Only "bug" should open a Jira ticket.

Analyzer is TRIGGER-PRECISE: it must not fire on benign setup lines like
"loop-task watchdog armed" or "LittleFS ready" (those would spam Jira).
"""
import argparse, json, sys, time, re

# REAL fault signatures (trigger events, not setup/announcement lines).
FAULT = [
    ("panic",        re.compile(r"Guru Meditation|abort\(\) was called|assert failed|"
                                r"StoreProhibited|LoadProhibited|InstrFetchProhibited|IllegalInstruction|"
                                r"Backtrace:"), "bug"),
    ("watchdog_fire",re.compile(r"Task watchdog got triggered|task_wdt: Task .* did not reset|"
                                r"wdt timeout|Interrupt wdt timeout|TG0WDT_SYS_RST|TG1WDT_SYS_RST"), "bug"),
    ("brownout",     re.compile(r"Brownout detector was triggered|rst:0x0f \(RTCWDT_BROWN"), "bug"),
    ("heap_corrupt", re.compile(r"CORRUPT HEAP|heap_caps.*assert|Cache disabled but cached memory"), "bug"),
    ("boot_fail",    re.compile(r"invalid segment length|No bootable app partitions|"
                                r"Factory app partition is not bootable"), "bug"),
    ("fs_error",     re.compile(r'partition "spiffs" could not be found|LittleFS mount failed|'
                                r"LittleFS mount/format"), "bug"),
    ("mbedtls_err",  re.compile(r"mbedtls_.*returned -0x|ssl_handshake.*failed|X509 - "), "warn"),
    ("mqtt_fail",    re.compile(r"MQTT connect failed|mqtt.*rc=-?\d|CONNECT refused"), "warn"),
    ("wifi_fail",    re.compile(r"WiFi.*disconnect reason|AUTH_FAIL|NO_AP_FOUND|beacon timeout"), "warn"),
]
HEAP_RE  = re.compile(r"free heap:\s*(\d+)")
RESET_RE = re.compile(r"rst:0x[0-9a-fA-F]+ \(([^)]+)\)")

def capture(port, secs, baud=115200):
    try:
        import serial
    except ImportError:
        return None, "pyserial-missing"
    try:
        s = serial.Serial(port, baud, timeout=0.3)
    except Exception as e:
        return None, f"open-failed:{e}"
    # reset on open (WCH bridge) -> fresh boot = the interaction
    try:
        s.setDTR(False); s.setRTS(True); time.sleep(0.1); s.setRTS(False)
    except Exception:
        pass
    buf = b""; t0 = time.time()
    while time.time() - t0 < secs:
        try:
            d = s.read(4096)
        except Exception:
            break
        if d: buf += d
    try: s.close()
    except Exception: pass
    return buf.decode("utf-8", "replace"), None

def analyse(txt):
    findings = []
    severity = "ok"
    for sig, rx, sev in FAULT:
        hits = rx.findall(txt)
        if hits:
            findings.append({"sig": sig, "count": len(hits),
                             "sample": (hits[0] if isinstance(hits[0], str) else str(hits[0]))[:80]})
            if sev == "bug": severity = "bug"
            elif sev == "warn" and severity != "bug": severity = "warn"
    heaps = [int(x) for x in HEAP_RE.findall(txt)]
    # Within ONE boot window heap naturally drops as BLE+crypto+provisioning allocate
    # (steady-state, NOT a leak) — so do NOT flag relative decline here. Only flag
    # a critically LOW free heap (near-OOM). Cross-cycle leak trend is computed by the
    # Go monitor comparing heap_min across successive checks over hours.
    if heaps and min(heaps) < 25000:
        findings.append({"sig": "heap_critical", "count": 1,
                         "sample": f"min free {min(heaps)}B (<25KB, near-OOM)"})
        severity = "bug"
    # reset-loop: >2 ROM boots in the window with no explicit reset interaction is abnormal
    roms = txt.count("ESP-ROM:esp32")
    if roms > 2:
        findings.append({"sig": "reset_loop", "count": roms, "sample": f"{roms} ROM boots in window"})
        severity = "bug"
    return findings, severity, heaps

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", required=True)
    ap.add_argument("--name", default="device")
    ap.add_argument("--secs", type=int, default=18)
    a = ap.parse_args()
    txt, err = capture(a.port, a.secs)
    out = {"port": a.port, "name": a.name, "captured_at": int(time.time())}
    if txt is None:
        out.update({"ok": False, "error": err, "severity": "warn", "findings": [],
                    "log_tail": "", "booted": False})
        print(json.dumps(out)); return
    findings, severity, heaps = analyse(txt)
    booted = ("ESP-ROM:esp32" in txt)
    out.update({
        "ok": True,
        "booted": booted,
        "setup_complete": ("Setup: Complete" in txt),
        "littlefs_ok": ("LittleFS ready" in txt),
        "crypto_ok": ("interop OK" in txt),
        "provisioning": ("BLE advertising" in txt or "provisioning" in txt.lower()),
        "heap_min": (min(heaps) if heaps else None),
        "heap_max": (max(heaps) if heaps else None),
        "severity": ("bug" if (booted and not out_setup(txt)) else severity),
        "findings": findings,
        "bytes": len(txt),
        "log_tail": txt[-2200:],
    })
    print(json.dumps(out))

def out_setup(txt):
    # helper: did setup finish? (a boot that never reaches Setup: Complete is itself a bug)
    return "Setup: Complete" in txt

if __name__ == "__main__":
    main()
