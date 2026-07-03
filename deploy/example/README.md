# Example bootstrap

`cwv2.yaml` + `resources.yaml` make the Example project runnable — the **only remaining action is
entering credentials** (secret names resolve via Keychain/Azure KV/env; no values are stored here).

## To go live
1. Populate the secrets named in `cwv2.yaml` (Jira email+token, Control Plane token) and log in each
   Claude account's `config_dir`.
2. Set per-repo build/verify commands (comments in `cwv2.yaml`).
3. Run `../live-acceptance.sh deploy/example/cwv2.yaml` (validate → live acceptance run).

Generated from the V1 migration (`docs/reports/PHASE_A_V1_MIGRATION.md`).
