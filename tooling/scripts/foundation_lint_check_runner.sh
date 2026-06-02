#!/bin/zsh
set -euo pipefail

if [[ "$#" -lt 2 ]]; then
  echo "usage: foundation_lint_check_runner.sh <log-file> <command> [args...]" >&2
  exit 2
fi

log_file="$1"
shift
timeout_sec="${FOUNDATION_LINT_CHECK_TIMEOUT_SEC:-600}"

if command -v perl >/dev/null 2>&1; then
  perl -e '
    use strict;
    use warnings;
    use POSIX ":sys_wait_h";

    my $log_file = shift @ARGV;
    my $timeout = shift @ARGV;
    my @cmd = @ARGV;
    my $pid = fork();
    die "fork failed: $!\n" unless defined $pid;

    if ($pid == 0) {
      setpgrp(0, 0) or die "setpgrp failed: $!\n";
      open STDOUT, ">", $log_file or die "open $log_file failed: $!\n";
      open STDERR, ">&", \*STDOUT or die "redirect stderr failed: $!\n";
      exec @cmd or die "exec failed: $!\n";
    }

    my $deadline = time() + $timeout;
    my $next_heartbeat = time() + 10;
    my $status;

    while (1) {
      my $done = waitpid($pid, WNOHANG);
      if ($done == $pid) {
        $status = $?;
        last;
      }
      if ($done == -1) {
        print STDERR "waitpid failed: $!\n";
        exit 1;
      }
      if (time() >= $deadline) {
        kill "TERM", -$pid;
        select(undef, undef, undef, 0.5);
        kill "KILL", -$pid;
        waitpid($pid, 0);
        print STDERR "check timed out after ${timeout}s\n";
        exit 124;
      }
      if (time() >= $next_heartbeat) {
        print STDERR "[WAIT] @cmd\n";
        $next_heartbeat = time() + 10;
      }
      select(undef, undef, undef, 0.2);
    }

    if (!defined $status) {
      print STDERR "check timed out after ${timeout}s\n";
      exit 1;
    }
    if ($status & 127) {
      exit 128 + ($status & 127);
    }
    exit($status >> 8);
  ' "$log_file" "$timeout_sec" "$@"
else
  "$@" >"$log_file" 2>&1
fi
