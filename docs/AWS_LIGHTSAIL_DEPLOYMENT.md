# Deploy Qwish on Lightsail with direct Amazon S3 media

The API runs in Docker behind Caddy HTTPS. Images are uploaded to and read directly from a dedicated S3 public media bucket. Supabase remains the PostgreSQL and authentication service. No R2 objects need migrating.

## 1. Set up S3

Create a standard S3 bucket in the chosen region, preferably near the existing Supabase database and Lightsail instance. Use a unique bucket name without dots for straightforward regional HTTPS URLs. Enable versioning, use default SSE-S3 encryption, and retain Bucket owner enforced ownership with ACLs disabled.

Templates in `deploy/s3/` provide:

- `bucket-policy.json`: anonymous object reads and a requirement for HTTPS. Replace `BUCKET_NAME` before applying it. The bucket is exclusively for public media; private documents require a separate private bucket.
- `runtime-policy.json`: bucket-scoped object reads, uploads, and deletion for the backend IAM principal. Replace `BUCKET_NAME`; attach to a dedicated IAM application user for this Lightsail instance.
- `public-access-block.json`: blocks public ACLs but permits the public read bucket policy. Effective account settings must also permit it; changing account settings affects other buckets, so review that scope before changing them. Anonymous upload, deletion, and listing are not granted.
- `cors.json`: frontend origins, GET/HEAD/PUT, Content-Type and S3 headers. Adjust the origins to match your actual clients. Upload credentials remain on the server; browsers receive five-minute presigned PUT URLs.

These files are AWS CLI JSON shapes: apply with `put-bucket-policy`, `put-public-access-block`, and `put-bucket-cors` using the corresponding `--policy`, `--public-access-block-configuration`, or `--cors-configuration` argument. [AWS public read configuration](https://docs.aws.amazon.com/AmazonS3/latest/userguide/granting-public-access.html) and [S3 CORS](https://docs.aws.amazon.com/AmazonS3/latest/userguide/cors.html) describe the settings.

Provide AWS credentials through `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`; temporary credentials also require `AWS_SESSION_TOKEN`. Local development can instead use the SDK's shared credential profiles. Initialization fails clearly if credentials cannot be loaded; it does not probe S3 permissions or verify the bucket exists. Check those with the upload acceptance steps below.

`AWS_REGION` and `S3_BUCKET_NAME` are required. `S3_PUBLIC_URL` is optional: the API derives `https://<bucket>.s3.<region>.amazonaws.com`. The upload API's existing paths and response fields are preserved.

## 2. Prepare the Lightsail instance

Create an Ubuntu Lightsail instance with an initial budget of about 2 GB RAM. Attach a static IP. Permit inbound TCP 80/443 and restrict SSH to your administration addresses. Keep 8080 closed in the public firewall. Review IPv4 and IPv6 rules separately. Point your API hostname's DNS A record at the static IP; remove stale AAAA records if IPv6 is not configured.

Install Docker Engine with the Compose plugin using the [official Ubuntu instructions](https://docs.docker.com/engine/install/ubuntu/). This deployment requires Compose **2.30.0 or newer** for raw environment files. Install `curl` for the host cron runner. Enable Docker at boot.

Copy the repository's `deploy/` directory to the host, for example under `/opt/qwish/deploy`, and retain the same directory for releases so Caddy certificate volumes remain attached to the same Compose project.

```bash
cd /opt/qwish/deploy
cp deploy.env.example deploy.env
cp api.env.example api.env
chmod 600 deploy.env api.env
```

Set `API_IMAGE`, `API_HOSTNAME`, and `ACME_EMAIL` in `deploy.env`. Fill `api.env` with real Supabase, S3, Resend, FCM, cron, origins, and optional integration settings from the existing environment. Production requires FCM configuration and an explicit origin list in this codebase. Preserve the existing Android passkey origin if used.

`api.env` uses raw `KEY=value` syntax: do not wrap values in quotes. Keep FCM/Play Integrity JSON on a single line. Raw loading preserves dollar signs in passwords and JSON. Environment files are ignored by Git and excluded from image builds. The local development `.env.example` uses normal dotenv syntax instead.

The API container has a 1 GB memory limit with `GOMEMLIMIT=800MiB` by default; adjust both in `deploy.env` if sizing changes. It runs as an unprivileged user with a read-only filesystem and writable temporary storage. API port 8080 is published only on host loopback.

## 3. Build and start the API

`.github/workflows/container.yml` builds multi-platform images in GHCR on pushes to `main`, version tags, and manual runs. Releases receive a `sha-<full-commit-sha>` tag; pin that tag or a digest in `API_IMAGE`. If the repository uses another default branch, update the workflow trigger. Private GHCR packages require authenticating Docker on the host with a package-read credential using `docker login --password-stdin`.

For a manual build from the repository root on a machine with Docker:

```bash
docker build -t your-registry/qwish-backend:your-release .
docker push your-registry/qwish-backend:your-release
```

Before startup, back up the database and inspect pending migrations: the API applies migrations and performs a ratings backfill at boot, including when pointed at production during a preview. Keep the existing compatible Supabase session pooler configuration.

From the deployment directory:

```bash
bash deploy.sh
curl --fail http://127.0.0.1:8080/ready
curl --fail https://api.qwish.in/ready
```

The script validates Compose configuration, pulls the images, and waits up to ten minutes for API readiness. It records the previous API image in `previous-image`. `/health` checks process liveness; `/ready` checks the database with a two-second timeout. Caddy uses `/ready` for health checks, renews HTTPS certificates, and immediately flushes SSE responses. Public access to `/api/v1/internal/*` is rejected; timers call the API directly on loopback.

The API handles SIGTERM with a 25-second drain period. SSE streams and long cron jobs are closed if they outlive it. Expect a short interruption during a single-instance deployment.

## 4. Install and activate cron schedules

The six timer files preserve all schedules from `render.yaml`, in UTC, including the nightly chain order. Each group uses one systemd oneshot service, preventing overlapping runs of that group. Missed activations trigger once after downtime; every missed interval is not replayed. POSTs are not automatically retried, because a timed-out request may have already committed its side effects. Failed runs are visible in the journal and service status.

```bash
sudo bash cron/install.sh
sudoedit /etc/qwish/cron.env
```

Set the same `CRON_SECRET` as the API and retain `CRON_API_URL=http://127.0.0.1:8080`. The installer creates a dedicated service user, protects the credential file, and verifies the unit files. It does not start the timers.

Disable existing Render cron services first and leave the GitHub repository variable `ENABLE_GITHUB_CRON` unset or `false`. The GitHub scheduler now runs only when explicitly opted in; manual workflow dispatch remains available. Then activate these timers:

```bash
sudo systemctl enable --now qwish-cron-hourly.timer qwish-cron-nightly.timer qwish-cron-streak-nudges.timer qwish-cron-announcements.timer qwish-cron-weekly-snapshot.timer qwish-cron-weekly-digests.timer
systemctl list-timers 'qwish-cron-*'
sudo journalctl -u 'qwish-cron@*.service' --since today
```

For subsequent deployments, stop the six timers to prevent new activations, wait for active `qwish-cron@*.service` jobs to finish, deploy the API, and start the timers again. Do not interrupt an active job just to redeploy. Alerts for failed units should be connected to your existing operations monitoring destination.

## 5. Acceptance checks and operations

Validate with staging credentials and a staging database before production:

1. Sign in and refresh a token, then complete a representative quiz flow.
2. Use `/api/v1/upload/image` to upload JPEG/PNG/WebP and open the returned permanent S3 URL.
3. Use `/api/v1/upload/presign`, PUT the image to `upload_url` with the requested Content-Type, and display `public_url` from a browser. Confirm CORS succeeds from your frontend origin.
4. Confirm expired signatures and anonymous PUT/DELETE/list requests fail while public image GET succeeds. The presigned PUT path retains its existing behavior and does not enforce the multipart endpoint's 5 MB limit.
5. Verify notifications stream through HTTPS and that scheduled jobs run only under one scheduler.
6. Restart the container and reboot the host to confirm API, HTTPS, and timer recovery.

```bash
docker compose --env-file deploy.env -f compose.yaml ps
docker compose --env-file deploy.env -f compose.yaml logs --tail 100 api caddy
systemctl --failed
```

Enable Lightsail automatic snapshots, monitor host disk/memory and HTTP readiness, and retain Supabase backups. Docker log rotation is configured; configure journald retention on the host if needed. S3 versions incur storage charges, so choose noncurrent-version lifecycle retention based on recovery needs. S3 internet delivery is billed separately from the Lightsail instance.

## Rollback

Set `API_IMAGE` in `deploy.env` to the previous compatible **S3-enabled** release and run `bash deploy.sh`, or explicitly run `bash deploy.sh <previous-image>` and then align `deploy.env` with that image. The original R2-only release cannot upload to this S3 bucket. Keep uploaded S3 objects across application rollbacks.

For a hosting rollback, prepare the Render service with the S3-enabled release and environment variables before switching API DNS back. Disable Lightsail timers before re-enabling another scheduler. Database migrations are not undone by changing an image; older code must remain compatible with the applied schema.
