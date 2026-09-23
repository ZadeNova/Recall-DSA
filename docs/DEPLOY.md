# Deployment (SPEC.md §10)

Host setup checklist for the Raspberry Pi 4, plus how the automated
deploy pipeline works. Tailscale, firewall, and cron are host
configuration, not application concerns, so they're not codified in the
Dockerfile/compose file.

## One-time Pi setup

1. **Install Tailscale on the Pi** (not in Docker — see the design note
   in `docker-compose.yml`) and bring it up:
   ```
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up
   tailscale ip -4    # note this — you'll need it in step 3
   ```

2. **Firewall**: deny all inbound except SSH (key-only). The tailnet-IP
   port binding below is defense in depth, not a substitute for this.
   ```
   sudo ufw default deny incoming
   sudo ufw allow ssh
   sudo ufw enable
   ```

3. **Clone the repo and configure the tailnet IP**:
   ```
   git clone https://github.com/ZadeNova/Recall-DSA.git ~/Recall-DSA
   cd ~/Recall-DSA
   cp .env.example .env
   # edit .env, set TAILSCALE_IP to the address from step 1
   ```

4. **Decide on data.** A fresh `docker compose up` starts with an empty,
   auto-seeded database (the 35 standard NC150 topics, no problems). If
   you want to bring over data you've already added locally — a bulk
   import you tested with, say — do this *before* step 5:
   ```
   mkdir -p data
   # on your dev machine, with the local app stopped first (so WAL is
   # checkpointed cleanly):
   scp recall.db* pi@<host>:~/Recall-DSA/data/
   ```
   Skip this step entirely to start fresh.

5. **Pull and run** (the image is built by CI and pushed to GHCR — the
   Pi never needs a Go toolchain or `docker compose build`):
   ```
   docker compose pull
   docker compose up -d
   ```
   The app is now reachable at `http://<tailscale-ip>:8080` from any
   device on your tailnet, and only from your tailnet.

6. **Backups** (optional, but recommended — see `scripts/backup.sh` for
   why this is worth doing even on reliable storage). Requires a private
   git repo already cloned on the Pi with push access configured (SSH
   deploy key or PAT):
   ```
   crontab -e
   # add:
   0 3 * * * /path/to/Recall-DSA/scripts/backup.sh /path/to/Recall-DSA/data/recall.db /path/to/backup-repo
   ```

This is a one-time setup. Once it's done, deploys happen automatically —
see below.

## Automatic deploys (CI/CD)

Every push to `main` runs `.github/workflows/deploy.yml`, which:

1. **test** — runs `go vet` and `go test ./...`. Nothing downstream runs
   if this fails.
2. **build** — cross-compiles for arm64 natively (no QEMU — see the
   `Dockerfile`'s `--platform=$BUILDPLATFORM` builder stage) and pushes
   `ghcr.io/zadenova/recall-dsa:latest` to GHCR.
3. **deploy** — joins the tailnet as an ephemeral node (via a reusable
   Tailscale auth key, tagged `tag:ci`) and runs `scripts/deploy.sh` on
   the Pi over SSH. That script does `git pull` (picks up any
   non-image changes, including future edits to itself), `docker
   compose pull` (fetches the new image), `docker compose up -d`
   (restarts with it), and `docker image prune -f` (drops the now-
   dangling old image so the SSD doesn't accumulate layers).

**Nothing to do manually** for an ordinary code change — push to `main`
and the Pi is running the new version within a couple of minutes.

### Required GitHub repo secrets

| Secret | Purpose |
|---|---|
| `TS_AUTHKEY` | Lets the CI runner join the tailnet (reusable + ephemeral, tagged `tag:ci`) |
| `PI_SSH_KEY` | Private half of a dedicated deploy keypair (public half lives in the Pi's `authorized_keys`) |
| `PI_HOST` | The Pi's tailnet hostname (`pi4`) |
| `PI_USER` | The SSH user on the Pi (`zade`) |

**`TS_AUTHKEY` expires after 90 days** (Tailscale's max, and OAuth
clients — which wouldn't need rotation — aren't available on this
account's plan). Regenerate it in the Tailscale admin console
(Settings → Keys) with the same settings (reusable, ephemeral,
`tag:ci`) before it expires, and update the GitHub secret. A `deploy`
job failing with a Tailscale auth error is the symptom to watch for.

### Tailscale ACL notes

`tag:ci` and `tag:pi` are declared in `tagOwners`; `pi4` is tagged
`tag:pi`. Tagging a device removes it from `autogroup:self`, which is
why the default Tailscale SSH policy needed explicit `ssh` grants added
for `tag:ci → tag:pi` (CI) and `autogroup:member → tag:pi` (so you can
still SSH in yourself) — see the ACL's `"ssh"` block in the admin
console if either stops working after further ACL edits.

## Manual fallback

Useful when debugging CI itself, or for a first deploy before the
pipeline exists on a fresh checkout:

```
git pull
docker compose pull
docker compose up -d
```

The bind-mounted `./data/recall.db` is untouched by any of this — it
lives outside the image entirely.
