# Operations Bootstrap

This folder is project-owned. Foundation creates it once so each app can define production readiness without mixing app operations into Foundation Core.

## Delivery Metrics

CI emits a JSON event with DORA-ready fields through:

```bash
make delivery-metrics
```

Start by preserving the `delivery-metrics` CI artifact. When deployment is wired, forward those records to your metrics warehouse or observability backend and add deploy success/failure/rework signals from the deployment job.

Track:

1. Change lead time
2. Deployment frequency
3. Failed deployment recovery time
4. Change fail rate
5. Deployment rework rate

## Security Defaults

Production should keep:

1. `APP_ENV=production`
2. `REQUIRE_AUTH=true`
3. `PROTECT_OPERATIONAL_ENDPOINTS=true`
4. `ALLOWED_ORIGINS` set to exact origins only
5. `/metricsz`, `/metricsz/trace`, and operational event views behind authenticated operator/admin access

## Coolify and Compose Deployments

Production Compose bakes `config/postgresql.conf`, `config/pg_hba.conf`, and
`config/redis.conf` into small derived service images. Do not replace those
with host bind mounts unless the deploy flow first proves the host paths are
regular files and all parent directories are readable by the container user.

Coolify persistent application directories can create missing bind sources as
directories. That turns file mounts such as `./config/postgresql.conf` into
directory mounts and can break Postgres startup or Redis configuration silently.
Keep the baked-image default for production deployments.

Set `ALLOWED_ORIGINS` explicitly to the deployed frontend origin. If the deploy
platform exposes an FQDN variable, map it to `ALLOWED_ORIGINS` in the platform
environment rather than weakening the application default.

Postgres credentials are fixed when the volume is first initialized. Changing
`DB_PASSWORD`, `POSTGRES_PASSWORD`, or `DATABASE_URL` later does not rewrite the
existing `postgres` role password. If Coolify already initialized the volume,
either keep the env password aligned with that existing role password or rotate
the role password inside Postgres before redeploying.

The baked `pg_hba.conf` keeps Unix-socket access inside the Postgres container
trusted for operator recovery. TCP clients, including app and migration
containers, still require SCRAM authentication.

## Network Tuning for Mobile Clients

These settings apply where the browser TCP connection ends: the frontend
container, or the load balancer in front of it. The measurements are in
`docs/foundation/foundation_benchmarks.md`.

1. `tcp_slow_start_after_idle = 0` is set per container in `docker-compose.yml`.
   A new network namespace does not inherit it from the host.
2. BBR is a host setting. Containers created after the change inherit it. On a
   lossy 3G link it made a 269 KB push 34 to 48 percent faster than cubic.

Run on the host, then recreate the containers:

```bash
sudo modprobe tcp_bbr
echo tcp_bbr | sudo tee /etc/modules-load.d/bbr.conf
printf 'net.ipv4.tcp_congestion_control = bbr\n' | sudo tee /etc/sysctl.d/90-bbr.conf
sudo sysctl --system
```

NOTE: Do not set `tcp_congestion_control` in Compose. A container cannot use an
algorithm that the host kernel does not allow, so the container fails to start.

NOTE: A CDN or managed load balancer that terminates TCP controls these settings
itself. The host settings then apply only to the connection behind it.

WARNING: On Coolify, TLS ends at `coolify-proxy`, so the browser TCP connection
ends in that container, not in the frontend. The `docker-compose.yml` sysctl
then does not reach browser traffic. Add the same sysctl to the proxy service
in Coolify's proxy Compose file (`/data/coolify/proxy/docker-compose.yml`, also
on the server's Proxy page), then restart the proxy. Restart it after the host
BBR change too, because a container keeps the congestion control it started with:

```yaml
services:
  coolify-proxy:
    sysctls:
      net.ipv4.tcp_slow_start_after_idle: 0
```

## Incident Records

Use `incident_record_template.md` for production-impacting failures, failed deployments, rollbacks, and security-relevant events.
