#!/bin/zsh
set -euo pipefail

if [[ "$#" -lt 2 ]]; then
  echo "usage: foundation_lint_check_runner.sh <log-file> <command> [args...]" >&2
  exit 2
fi

log_file="$1"
shift
timeout_sec="${FOUNDATION_LINT_CHECK_TIMEOUT_SEC:-180}"

if command -v perl >/dev/null 2>&1; then
  perl -e '
    use strict;
    use warnings;

    my $timeout = shift @ARGV;
    my @cmd = @ARGV;
    my $pid = fork();
    die "fork failed: $!\n" unless defined $pid;

    if ($pid == 0) {
      setpgrp(0, 0) or die "setpgrp failed: $!\n";
      exec @cmd or die "exec failed: $!\n";
    }

    my $timed_out = 0;
    local $SIG{ALRM} = sub {
      $timed_out = 1;
      kill "TERM", -$pid;
      select(undef, undef, undef, 0.5);
      kill "KILL", -$pid;
    };

    alarm $timeout;
    waitpid($pid, 0);
    my $status = $?;
    alarm 0;

    if ($timed_out) {
      print STDERR "check timed out after ${timeout}s\n";
      exit 124;
    }

    if ($status == -1) {
      print STDERR "waitpid failed: $!\n";
      exit 1;
    }
    if ($status & 127) {
      exit 128 + ($status & 127);
    }
    exit($status >> 8);
  ' "$timeout_sec" "$@" >"$log_file" 2>&1
else
  "$@" >"$log_file" 2>&1
fi
