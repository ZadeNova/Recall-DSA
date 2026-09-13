# Deployment (SPEC.md §10)

Host setup checklist for the Raspberry Pi 4. This covers what isn't (and
can't be) codified in the Dockerfile/compose file — Tailscale, firewall,
and cron are host configuration, not application concerns.

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

3. **Configure the tailnet IP**:
   ```
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
   scp recall.db* pi@<host>:/path/to/Recall-DSA/data/
   ```
   Skip this step entirely to start fresh.

5. **Build and run**:
   ```
   docker compose build
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

## Updating after a code change

```
git pull
docker compose build
docker compose up -d
```

The bind-mounted `./data/recall.db` is untouched by any of this — it
lives outside the image entirely.
