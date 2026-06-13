@0xb8f2f7e22de44001;

struct HotPathEnvelope {
  correlationId @0 :Text;
  causationId @1 :Text;
  idempotencyKey @2 :Text;
  eventType @3 :Text;
  organizationId @4 :Text;
  source @5 :Text;
  eventTimeUnixNanos @6 :UInt64;
  processingTimeUnixNanos @7 :UInt64;
}
