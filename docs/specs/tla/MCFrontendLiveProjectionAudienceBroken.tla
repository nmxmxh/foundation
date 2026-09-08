------------------ MODULE MCFrontendLiveProjectionAudienceBroken -------------
EXTENDS Naturals, Sequences

\* NEGATIVE CONTROL for AudienceScopeStable.
\*
\* Reuses the real spec via INSTANCE and injects the exact defect the audience
\* mechanism exists to close: a delivery path that authorizes on tenant, domain
\* and collection but NOT on audience — which is what the projection gateway
\* did when its only trust boundary was the tenant. Every subscriber in the
\* organization then receives every record of the scopes it binds, so a foreign
\* record becomes resident in the store and AudienceScopeStable MUST be
\* violated.

CONSTANTS Tenant, OtherTenant, Domain, Collection, Record, NULL, Audience, OtherAudience

MaxQueued == 1
MaxVersion == 2

VARIABLES store, status, buffered, liveQueue, lastVersion, applied, rejected, dropped,
          residentAudience

INSTANCE FrontendLiveProjection

\* Like Accept, minus the audience conjunct: scope-only authorization.
TenantOnlyAccept(m) ==
  /\ m.tenant = Tenant
  /\ m.domain = Domain
  /\ m.collection = Collection
  /\ m.version >= lastVersion

\* Like ApplyAccepted, but gated on the scope-only predicate.
BadApplyTenantScoped(m) ==
  /\ TenantOnlyAccept(m)
  /\ store' = [store EXCEPT ![m.recordId] = IF m.op = "delete" THEN NULL ELSE m.record]
  /\ residentAudience' = [residentAudience EXCEPT ![m.recordId] = IF m.op = "delete" THEN NULL ELSE m.audience]
  /\ lastVersion' = m.version
  /\ applied' = applied + 1
  /\ UNCHANGED <<status, buffered, liveQueue, rejected, dropped>>

BrokenNext == Next \/ (\E m \in Mutation : BadApplyTenantScoped(m))

BrokenSpec == Init /\ [][BrokenNext]_vars

StateConstraint ==
  /\ applied <= 2
  /\ rejected <= 2
  /\ dropped <= 2
  /\ Len(buffered) <= MaxQueued

==============================================================================
