"""Adopt retention configuration without replacing application composition roots."""

import argparse
from pathlib import Path
import re
import subprocess

WORKER_IMPORT = 'workerkit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"'


def policy(indent):
    return (f"{indent}workerkit.ApplyRetentionPolicy(riverCfg, workerkit.RetentionPolicyForTier(workerkit.RetentionTier(cfg.RiverRetentionTier)))\n"
            f"{indent}if cfg.RiverCompletedJobRetention > 0 {{\n"
            f"{indent}\triverCfg.CompletedJobRetentionPeriod = cfg.RiverCompletedJobRetention\n"
            f"{indent}}}\n")


def patch_config(source):
    if "RiverRetentionTier" in source:
        return source
    if "type Config struct {" not in source or "getEnv(" not in source:
        raise ValueError("unsupported configuration shape")
    source = source.replace("type Config struct {", "type Config struct {\n"
                            "\tRiverRetentionTier string\n\tRiverCompletedJobRetention time.Duration\n", 1)
    source, count = re.subn(r"&Config\{", '&Config{\n'
                           '\t\tRiverRetentionTier: getEnv("RIVER_RETENTION_TIER", "high_frequency"),\n'
                           '\t\tRiverCompletedJobRetention: getEnvDuration("RIVER_COMPLETED_JOB_RETENTION", 0),',
                           source, count=1)
    if count != 1:
        raise ValueError("missing configuration initializer")
    if not re.search(r'"time"', source):
        source = source.replace("import (", 'import (\n\t"time"', 1)
    if "func getEnvDuration(" not in source:
        source += '''
func getEnvDuration(key string, fallback time.Duration) time.Duration {
    if value := os.Getenv(key); value != "" {
        if duration, err := time.ParseDuration(value); err == nil {
            return duration
        }
    }
    return fallback
}
'''
    return source


def patch_client(source):
    if "river.NewClient(" not in source:
        return source
    if "cfg.RiverRetentionTier" in source:
        return source
    source = source.replace("initWorkerEngine(ctx, deps.Projected,", "initWorkerEngine(ctx, cfg, deps.Projected,")
    source = source.replace("initWorkerEngine(ctx context.Context, projected", "initWorkerEngine(ctx context.Context, cfg *config.Config, projected")
    source = re.sub(r"^\s*workerkit.ApplyRetentionPolicy\(riverCfg, workerkit.RetentionPolicyForTier\(workerkit.RetentionTierHighFrequency\)\)\n", "\n", source, flags=re.M)
    inline = re.compile(r"(?P<i>\t+)(?P<v>\w+), (?P<e>\w+) := river.NewClient\((?P<driver>riverpgxv5.New\([^\n]+?\)), &river.Config\{\n(?P<body>.*?)\n(?P=i)\}\)", re.S)
    def replace(match):
        indent = match['i']
        return (f"{indent}riverCfg := &river.Config{{\n{match['body']}\n{indent}}}\n"
                + policy(indent) + f"{indent}{match['v']}, {match['e']} := river.NewClient({match['driver']}, riverCfg)")
    source, count = inline.subn(replace, source)
    if count == 0:
        local = re.compile(r"(?P<i>\t+)riverCfg := &river.Config\{(?P<body>[^{}]*?)\}(?P<end>\n)")
        source, count = local.subn(lambda m: m[0] + policy(m['i']), source)
    if count != 1:
        raise ValueError("expected one supported River client")
    if WORKER_IMPORT not in source:
        if '"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"' in source:
            raise ValueError("unsupported worker import alias")
        source = source.replace("import (", "import (\n\t" + WORKER_IMPORT + "\n", 1)
    return source


def apply(target):
    cfg = target / "internal/config/config.go"
    clients = [target / name for name in ["cmd/worker/main.go", "internal/startup/dependencies.go"]]
    clients = [path for path in clients if path.exists() and "river.NewClient(" in path.read_text()]
    if not clients:
        return []
    if not cfg.exists():
        raise ValueError("River client has no application configuration")
    pending = {cfg: patch_config(cfg.read_text())}
    pending.update({path: patch_client(path.read_text()) for path in clients})
    rendered = {}
    for path, source in pending.items():
        result = subprocess.run(["gofmt"], input=source, text=True, capture_output=True, timeout=10)
        if result.returncode:
            raise ValueError(f"invalid Go syntax in {path.name}")
        if result.stdout != path.read_text():
            rendered[path] = result.stdout
    for path, source in rendered.items():
        path.write_text(source)
    example = target / ".env.example"
    if example.exists():
        original = example.read_text()
        updated = original
        for key, value in [("RIVER_RETENTION_TIER", "high_frequency"), ("RIVER_COMPLETED_JOB_RETENTION", "")]:
            if not re.search(rf"^{key}=", updated, re.M):
                updated += f"\n{key}={value}\n"
        if original != updated:
            example.write_text(updated)
            rendered[example] = updated
    return [str(path.relative_to(target)) for path in rendered]


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", type=Path)
    args = parser.parse_args()
    try:
        for changed in apply(args.target):
            print(changed)
    except (ValueError, subprocess.TimeoutExpired) as error:
        parser.exit(1, f"retention migration refused: {error}\n")
