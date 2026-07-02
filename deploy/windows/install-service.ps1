# ClaudWorker V2 — Windows Service (via the built-in Service Control Manager).
# Requires an Administrator PowerShell. Uses sc.exe; for auto-restart use `sc failure`.
$bin = "C:\Program Files\cwv2\cwv2.exe"
$cfg = "C:\ProgramData\cwv2\cwv2.yaml"
$args = "serve --config `"$cfg`" --mode live --bind 127.0.0.1:8080 --web `"C:\Program Files\cwv2\web\ops-console`""
# validate config before installing
& $bin validate --config $cfg; if ($LASTEXITCODE -ne 0) { throw "config validation failed" }
sc.exe create cwv2 binPath= "`"$bin`" $args" start= auto DisplayName= "ClaudWorker V2"
sc.exe failure cwv2 reset= 86400 actions= restart/5000/restart/5000/restart/5000   # automatic recovery
sc.exe description cwv2 "ClaudWorker V2 autonomous engineering platform"
sc.exe start cwv2
