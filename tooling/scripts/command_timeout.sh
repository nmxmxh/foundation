#!/bin/bash

# Stop a command's process group when its deadline expires.
run_with_timeout() {
  local timeout_sec="${1:-}"
  if [[ "$#" -gt 0 ]]; then shift; fi
  if [[ ! "$timeout_sec" =~ ^[1-9][0-9]{0,4}$ ]] || (( timeout_sec > 86400 )) || [[ "$#" -eq 0 ]]; then
    echo 'command timeout requires 1..86400 seconds and a command' >&2
    return 64
  fi
  if ! command -v perl >/dev/null 2>&1; then
    echo 'Perl is required to enforce the command deadline' >&2
    return 127
  fi
  perl -e '
    use strict;
    use warnings;
    use Errno qw(EACCES ESRCH);
    use POSIX ":sys_wait_h";
    use Time::HiRes qw(clock_gettime CLOCK_MONOTONIC sleep);

    my $timeout = shift @ARGV;
    my $pid = fork();
    die "fork failed: $!\n" unless defined $pid;
    if ($pid == 0) {
      setpgrp(0, 0) or die "setpgrp failed: $!\n";
      exec { $ARGV[0] } @ARGV or do {
        print STDERR "command execution failed: $!\n";
        exit 127;
      };
    }
    if (!setpgrp($pid, $pid) && $! != EACCES && $! != ESRCH) {
      my $error = "$!";
      kill "KILL", $pid;
      waitpid($pid, 0);
      die "setpgrp failed: $error\n";
    }
    my $deadline = clock_gettime(CLOCK_MONOTONIC) + $timeout;
    my $cancelled = 0;
    $SIG{INT} = sub { $cancelled = 130; $deadline = 0; };
    $SIG{TERM} = sub { $cancelled = 143; $deadline = 0; };
    while (clock_gettime(CLOCK_MONOTONIC) < $deadline) {
      my $done = waitpid($pid, WNOHANG);
      if ($done == $pid) {
        my $status = $?;
        exit(($status & 127) ? 128 + ($status & 127) : $status >> 8);
      }
      if ($done == -1) {
        print STDERR "waitpid failed: $!\n";
        exit 1;
      }
      sleep 0.05;
    }
    kill "TERM", -$pid;
    sleep 0.5;
    kill "KILL", -$pid;
    waitpid($pid, 0);
    print STDERR "command timed out after ${timeout}s\n" unless $cancelled;
    exit($cancelled || 124);
  ' "$timeout_sec" "$@"
}
