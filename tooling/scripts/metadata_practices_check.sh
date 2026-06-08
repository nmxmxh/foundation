#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

perl -MFile::Find -MFile::Spec -MFile::Basename - "$target" <<'PERL'
  use strict;
  use warnings;

  my $target = File::Spec->rel2abs(shift @ARGV);
  my @violations;
  my %skip_parts = map { $_ => 1 } qw(foundation vendor node_modules .cache dist build generated testdata);

  sub ok { print "[OK] $_[0]\n"; }

  sub rel {
    my ($path) = @_;
    $path =~ s/^\Q$target\E\/?//;
    return $path eq "" ? "." : $path;
  }

  sub should_skip {
    my ($path) = @_;
    return 1 if $path =~ /_test[.]go\z/;
    for my $part (File::Spec->splitdir($path)) {
      return 1 if $skip_parts{$part};
    }
    return 0;
  }

  sub read_file {
    my ($path) = @_;
    open my $fh, "<", $path or die "open $path failed: $!\n";
    local $/;
    return <$fh>;
  }

  sub find_matching_brace {
    my ($text, $open_index) = @_;
    my $depth = 0;
    my $in_string = "";
    my $escaped = 0;
    for (my $idx = $open_index; $idx < length($text); $idx++) {
      my $ch = substr($text, $idx, 1);
      if ($in_string ne "") {
        if ($escaped) {
          $escaped = 0;
          next;
        }
        if ($ch eq "\\") {
          $escaped = 1;
          next;
        }
        if ($ch eq $in_string) {
          $in_string = "";
        }
        next;
      }
      if ($ch eq q{"} || $ch eq q{'} || $ch eq q{`}) {
        $in_string = $ch;
        next;
      }
      if ($ch eq "{") {
        $depth++;
      } elsif ($ch eq "}") {
        $depth--;
        return $idx if $depth == 0;
      }
    }
    return -1;
  }

  sub line_for {
    my ($text, $index) = @_;
    return 1 + (substr($text, 0, $index) =~ tr/\n//);
  }

  sub scan_go_file {
    my ($path) = @_;
    return if should_skip($path);
    my $text = read_file($path);
    my $rel = rel($path);

    while ($text =~ /events[.]Envelope\s*\{/g) {
      my $start = $-[0];
      my $open = index($text, "{", $start);
      my $close = find_matching_brace($text, $open);
      next if $close < 0;
      my $block = substr($text, $open + 1, $close - $open - 1);
      next if $block !~ /\S/;
      my $line = line_for($text, $start);
      if ($block !~ /\bMetadata\s*:/) {
        next if index($rel, "internal/service") >= 0;
        push @violations, "$rel:$line: events.Envelope must carry canonical metadata";
        next;
      }
      if ($block =~ /\bMetadata\s*:\s*(?:nil|map\s*\[\s*string\s*\]\s*(?:any|interface\s*\{\s*\})\s*\{)/) {
        push @violations, "$rel:$line: events.Envelope metadata must come from server-kit metadata helpers, not raw map literals";
      }
    }

    return if index($rel, "internal/service") < 0;
    while ($text =~ /metadata[.]FromContext\(ctx\)[.]ToMap\(\)/g) {
      push @violations, "$rel:" . line_for($text, $-[0]) . ": use metadata.FromContextMap(ctx, domainMetadata...) so tags/categories merge centrally";
    }
    while ($text =~ /\bMetadata\s*:\s*req[.]Metadata\b/g) {
      push @violations, "$rel:" . line_for($text, $-[0]) . ": request metadata must be merged with context metadata via metadata.FromContextMap(ctx, req.Metadata)";
    }
    while ($text =~ /\bMetadata\s*:\s*map\s*\[\s*string\s*\]\s*(?:any|interface\s*\{\s*\})\s*\{\s*\}/g) {
      push @violations, "$rel:" . line_for($text, $-[0]) . ": persisted domain metadata must inherit request metadata or carry explicit domain metadata, not an empty map";
    }
    while ($text =~ /\btags\s*:\s*\[\]string\s*\{([^}]*)\}/gisg) {
      if ($1 =~ /\b(authorization|bearer|cookie|jwt|password|private_key|secret|session_token|token)\b/i) {
        push @violations, "$rel:" . line_for($text, $-[0]) . ": metadata tags must not contain credentials, tokens, cookies, or secret-bearing values";
      }
    }
  }

  my @roots = (
    "$target/internal/service",
    "$target/backend/internal/service",
    "$target/internal/server",
    "$target/backend/internal/server",
  );
  for my $root (@roots) {
    next unless -d $root;
    find({ wanted => sub { scan_go_file($File::Find::name) if -f $_ && $_ =~ /[.]go\z/ }, no_chdir => 1 }, $root);
  }

  if (@violations) {
    print "[FAIL] service event metadata practices\n";
    print "  events.Envelope emissions must preserve canonical request metadata via server-kit/go/metadata.\n";
    my $limit = @violations > 80 ? 80 : scalar @violations;
    print "  $violations[$_]\n" for 0 .. $limit - 1;
    print "  ... " . (@violations - 80) . " more\n" if @violations > 80;
    exit 1;
  }
  ok("service event metadata practices");

  for my $legacy_metadata (
    "$target/api/protos/common/v1/metadata.proto",
    "$target/api/protos/common/v1/common.proto",
  ) {
    push @violations, rel($legacy_metadata) . ": common metadata is deprecated; use foundation.v1.Metadata"
      if -e $legacy_metadata;
  }

  my $metadata_proto = "$target/api/protos/foundation/v1/metadata.proto";
  $metadata_proto = "$target/foundation/runtime-transport/protos/foundation/v1/metadata.proto" if !-e $metadata_proto;
  $metadata_proto = "$target/runtime-transport/protos/foundation/v1/metadata.proto" if !-e $metadata_proto;
  if (-e $metadata_proto) {
    my $proto_text = read_file($metadata_proto);
    push @violations, rel($metadata_proto) . ": foundation metadata must expose GlobalContext"
      if $proto_text !~ /\bmessage\s+GlobalContext\b/;
    push @violations, rel($metadata_proto) . ": foundation metadata must expose repeated string tags"
      if $proto_text !~ /\brepeated\s+string\s+tags\s*=/;
    push @violations, rel($metadata_proto) . ": foundation metadata must expose repeated string categories"
      if $proto_text !~ /\brepeated\s+string\s+categories\s*=/;
    push @violations, rel($metadata_proto) . ": foundation metadata must expose knowledge_graph for intelligence graph scope"
      if $proto_text !~ /\bstring\s+knowledge_graph\s*=/;
    push @violations, rel($metadata_proto) . ": foundation metadata must expose correlation_id"
      if $proto_text !~ /\bstring\s+correlation_id\s*=/;
    push @violations, rel($metadata_proto) . ": foundation metadata must expose idempotency_key"
      if $proto_text !~ /\bstring\s+idempotency_key\s*=/;
    push @violations, rel($metadata_proto) . ": foundation metadata must expose map<string,string> attributes for bounded graph facts"
      if $proto_text !~ /\bmap\s*<\s*string\s*,\s*string\s*>\s+attributes\s*=/;
  } else {
    push @violations, "api/protos/foundation/v1/metadata.proto: foundation metadata proto is required";
  }

  my $api_protos = "$target/api/protos";
  if (-d $api_protos) {
    find({
      wanted => sub {
        return unless -f $_ && $_ =~ /[.]proto\z/;
        my $path = $File::Find::name;
        my $text = read_file($path);
        my $rel = rel($path);
        push @violations, "$rel: common metadata imports are deprecated; import foundation/v1/metadata.proto"
          if $text =~ /import\s+"common\/v1\/metadata[.]proto"\s*;/;
        push @violations, "$rel: common RequestMetadata/ResponseMetadata is deprecated; use foundation.v1.Metadata"
          if $text =~ /\bcommon[.]v1[.](?:RequestMetadata|ResponseMetadata)\b/;
      },
      no_chdir => 1,
    }, $api_protos);
  }

  my $migrations = "$target/migrations";
  if (-d $migrations) {
    my $tag_query = 0;
    my $tag_index = 0;
    find({
      wanted => sub {
        return unless -f $_ && $_ =~ /[.]up[.]sql\z/;
        my $path = $File::Find::name;
        my $text = read_file($path);
        my $rel = rel($path);
        while ($text =~ /^[ \t]*metadata[ \t]+jsonb\b[^\n,]*(?:,|$)/gmi) {
          my $line_text = $&;
          my $upper = uc($line_text);
          if ($upper !~ /NOT NULL/ || $upper !~ /DEFAULT/ || $line_text !~ /'\{\}'/) {
            push @violations, "$rel:" . line_for($text, $-[0]) . ": metadata jsonb must be NOT NULL DEFAULT '\''{}'\''::jsonb";
          }
        }
        $tag_query = 1 if $text =~ /metadata\s*->\s*'tags'|metadata\s*->>\s*'tags'|metadata\s*@>/;
        $tag_index = 1 if $text =~ /USING\s+GIN\s*\(\s*\(?\s*metadata\s*(?:jsonb_path_ops|->\s*'tags'|)\s*\)?/i || $text =~ /metadata_tags.*GIN|tags.*GIN/i;
      },
      no_chdir => 1,
    }, $migrations);
    push @violations, "migrations: metadata tag queries require a matching GIN or metadata->'tags' expression index"
      if $tag_query && !$tag_index;
  }

  if (@violations) {
    print "[FAIL] metadata practices\n";
    print "  Metadata must be canonical, tag-capable, and queryable when tags are used.\n";
    my $limit = @violations > 100 ? 100 : scalar @violations;
    print "  $violations[$_]\n" for 0 .. $limit - 1;
    print "  ... " . (@violations - 100) . " more\n" if @violations > 100;
    exit 1;
  }
  ok("metadata proto/schema practices");
  print "metadata practices check passed\n";
PERL
