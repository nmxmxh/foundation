# ASD-STE100 Documentation and Comment Practices

Status: v1.0
Date: 2026-07-19
Owner: Platform Architecture
Practice: CP-37

> Distilled from ASD-STE100 Simplified Technical English Issue 8, adapted for
> software engineering documentation, code comments, and agent-authored prose
> across the Foundation system.

## Purpose

This practice defines how to write clear, bounded, unambiguous documentation
and code comments across all Foundation modules. The rules come from ASD-STE100
(Simplified Technical English), a controlled-language standard originally
designed for aerospace maintenance manuals. The same discipline — eliminate
ambiguity, bound complexity, enforce single-meaning words — applies directly to
technical documentation consumed by humans and AI agents.

## Scope

This practice applies to:

- All markdown files under `docs/`, `templates/`, and `agent_memory/`
- All code comments in Go, Rust, and TypeScript source files
- Agent-authored prose in handoff notes, evidence ledgers, and walkthroughs
- README files for all Foundation modules
- The `AGENTS.md` instruction file and all files it references

---

## Part 1: Controlled Vocabulary

### STE-01: Approved Words and Technical Names

Use each word only in its approved part of speech and approved meaning.

**Rule**: If a word has multiple dictionary meanings, choose the one that
matches its STE-approved sense. When no approved synonym exists, use a
Foundation-recognized technical name.

**Technical names** are nouns or noun phrases that identify specific system
concepts. They are always permitted as nouns when they appear in the Foundation
glossary or module API surface:

| Category | Examples |
| :--- | :--- |
| **Infrastructure nouns** | `CorrelationID`, `TenantID`, `RuntimeEnvelope`, `Hermes`, `River` |
| **Event lifecycle verbs** | `publish`, `subscribe`, `dispatch`, `enqueue`, `dequeue` |
| **Database verbs** | `migrate`, `query`, `insert`, `upsert`, `truncate` |
| **Transport verbs** | `serialize`, `deserialize`, `encode`, `decode`, `compress` |
| **Auth verbs** | `authenticate`, `authorize`, `validate`, `revoke` |
| **Runtime verbs** | `allocate`, `deallocate`, `dispatch`, `compile`, `instantiate` |
| **Observation verbs** | `trace`, `measure`, `record`, `instrument` |

**Anti-pattern — overloaded verbs**: Do not use `check` for both "verify
correctness" and "inspect visually." Choose `verify` or `inspect`.

---

### STE-02: American English Spelling

Use American English spelling in all documentation and code comments.

| Use | Do not use |
| :--- | :--- |
| `color` | `colour` |
| `initialize` | `initialise` |
| `behavior` | `behaviour` |
| `canceled` | `cancelled` |
| `license` | `licence` |
| `organization` | `organisation` |

---

## Part 2: Noun Clusters

### STE-03: Bound Noun Clusters to 3 Words

A noun cluster is a group of nouns or adjectives that functions as a single
noun phrase. Long noun clusters create ambiguity because the reader cannot tell
which word modifies which.

**Rule**: Write noun clusters of no more than 3 words. Expand longer clusters
with prepositions (`of`, `in`, `for`, `to`, `from`, `with`).

#### Foundation-Domain Examples

| Non-STE (too long) | STE (bounded) |
| :--- | :--- |
| user session authentication token validation | validation of user session tokens |
| realtime stream connection pool supervisor | supervisor for the connection pool |
| tenant organization database migration script | migration script for the tenant database |
| event bus subscription pattern matcher | pattern matcher for event subscriptions |
| WASM control buffer memory layout | memory layout of the control buffer |
| background worker retry policy configuration | configuration of the retry policy |
| projection freshness staleness threshold value | staleness threshold for projections |
| bulk transfer multipart upload progress tracker | progress tracker for multipart uploads |

#### Hyphenation Rule (STE-12)

Use hyphens to link compound modifiers that precede a noun:

- `tenant-scoped` keys (not: tenant scoped keys)
- `high-performance` compute (not: high performance compute)
- `zero-allocation` path (not: zero allocation path)
- `progress-bearing` transfer (not: progress bearing transfer)

---

## Part 3: Verbs and Voice

### STE-04: Imperative Form for Procedural Steps

Use the command form (imperative) for all procedural instructions, code
comments, and step-by-step documentation.

| Non-STE (passive/conditional) | STE (imperative) |
| :--- | :--- |
| The token should be validated before saving. | Validate the token before you save it. |
| Configuration values are loaded from env vars. | Load configuration values from environment variables. |
| Tests should be run before merging. | Run all tests before you merge. |
| The migration must be applied in order. | Apply the migration in the correct order. |
| The circuit breaker should be configured. | Configure the circuit breaker. |

### STE-05: Active Voice in Descriptions

Make the subject perform the action. Active voice is mandatory in procedural
writing and required in descriptive writing.

| Non-STE (passive) | STE (active) |
| :--- | :--- |
| The request is processed by the worker. | The worker processes the request. |
| The envelope is wrapped by runtime-transport. | Runtime-transport wraps the envelope. |
| Projections are rebuilt by Hermes. | Hermes rebuilds projections. |
| The schema is validated by the policy engine. | The policy engine validates the schema. |
| Events are published by the event bus. | The event bus publishes events. |

### STE-06: No Phrasal Verbs

A phrasal verb combines a verb with a preposition and introduces ambiguity.
Replace phrasal verbs with direct action verbs.

| Phrasal verb (do not use) | Direct verb (use) |
| :--- | :--- |
| set up | configure |
| carry out | execute |
| look into | investigate |
| bring up | start, raise |
| shut down | stop |
| put in | insert |
| come up with | create, design |
| go through | review, process |
| figure out | determine |
| break down | decompose, separate |
| hold off | defer |
| turn on / turn off | enable / disable |
| roll out | deploy |
| spin up | start, launch |
| tear down | destroy, remove |

---

## Part 4: Sentence Structure and Word Counts

### STE-07: Procedural Sentence Bounds (20 Words)

All procedural sentences must contain a maximum of 20 words.

This applies to:

- Code comments (Go `//`, Rust `///`, TypeScript `/** */`)
- Procedural steps in markdown documentation
- Agent handoff instructions
- Migration checklists

#### Counting rules

- Hyphenated words count as one word (`tenant-scoped` = 1 word).
- Text inside parentheses counts as one word in the main sentence.
- Articles (`a`, `the`, `an`) count toward the total.

#### Examples

**Non-STE** (28 words):
> When the worker receives a message from the event bus, it should validate the
> correlation ID and then check whether the tenant scope matches the expected
> organization context.

**STE** (two sentences, 12 and 11 words):
> The worker validates the correlation ID from the event bus message.
> Then verify that the tenant scope matches the expected organization.

### STE-08: Descriptive Sentence Bounds (25 Words)

All descriptive sentences must contain a maximum of 25 words.

This applies to:

- Architecture descriptions
- Module overview paragraphs
- Design rationale sections
- Philosophy and informational prose

### STE-09: Paragraph Bounds (6 Sentences)

Each paragraph must contain a maximum of 6 sentences. Start every paragraph
with a topic sentence that states the main point.

### STE-10: Condition-First Structure

When an instruction depends on a condition, state the condition first and
separate it with a comma.

| Non-STE | STE |
| :--- | :--- |
| Reject the request if the token is expired. | If the token is expired, reject the request. |
| Fall back to Postgres when Hermes is stale. | When Hermes is stale, fall back to Postgres. |
| Skip validation only when in test mode. | When in test mode, skip validation. |

### STE-11: One Topic Per Sentence

Each sentence must convey only one primary topic or action.

| Non-STE (multiple topics) | STE (separated) |
| :--- | :--- |
| Validate the token and check the tenant scope before dispatching the event to the worker queue. | Validate the token. Check the tenant scope. Then dispatch the event to the worker queue. |

---

## Part 5: Safety Instructions

### STE-13: Alert Taxonomy

Categorize all alerts strictly. Do not mix categories.

#### WARNING

Use `WARNING` when an action can cause:

- Security vulnerability or data breach
- Data destruction or corruption
- Memory corruption or undefined behavior
- Financial loss or incorrect ledger state

**Syntax**: State the mandatory command first. Follow with the risk.

```markdown
> [!WARNING]
> Do not expose the private key in logs. Exposure of the key breaches system security.
```

```markdown
> [!WARNING]
> Do not use floating-point arithmetic for ledger balances. Rounding errors cause financial discrepancies.
```

#### CAUTION

Use `CAUTION` when an action can cause:

- Software bugs or logic errors
- Performance degradation or latency spikes
- Race conditions or deadlocks
- Connection pool exhaustion

```markdown
> [!CAUTION]
> Do not execute complex regex in SQL queries. Heavy regex exhausts the database connection pool.
```

```markdown
> [!CAUTION]
> Do not hold locks across async boundaries. Holding locks causes deadlocks under load.
```

#### NOTE

Use `NOTE` only to provide supplementary information. Notes must never contain
mandatory instructions or safety warnings.

```markdown
> [!NOTE]
> The `hermes` module falls back to Postgres when projections exceed the staleness threshold.
```

---

## Part 6: Punctuation Rules

### STE-14: No Semicolons

Semicolons create complex, compound sentences. Divide clauses into separate
sentences with periods.

| Non-STE | STE |
| :--- | :--- |
| The worker retries the job; if it fails again, it moves to the dead-letter queue. | The worker retries the job. If the retry fails, the worker moves the job to the dead-letter queue. |

### STE-15: Colons for Vertical Lists

Introduce itemized lists with a colon. Capitalize the first letter of each
item.

```markdown
The event lifecycle has three terminal states:

- Requested: Command received, validation passed.
- Success: Operation completed.
- Failed: Operation failed with a reason.
```

### STE-16: Parentheses

Use parentheses for parameter names, short abbreviations, or version numbers.
Text in parentheses counts as one word in the main sentence but forms its own
internal sentence.

---

## Part 7: Anti-Patterns and Refactoring Rules

### STE-17: No Contractions

Do not use contractions. Write all words in full.

| Do not use | Use |
| :--- | :--- |
| `don't` | `do not` |
| `can't` | `cannot` |
| `won't` | `will not` |
| `shouldn't` | `should not` |
| `isn't` | `is not` |
| `doesn't` | `does not` |
| `it's` | `it is` or `it has` |
| `that's` | `that is` |
| `there's` | `there is` |
| `you're` | `you are` |
| `we're` | `we are` |
| `I'm` | `I am` |
| `let's` | `let us` |
| `we've` | `we have` |

### STE-18: No Latin Abbreviations

Do not use Latin abbreviations. Use English equivalents.

| Do not use | Use |
| :--- | :--- |
| `e.g.` | `for example` |
| `i.e.` | `that is` |
| `etc.` | explicit list, or `and similar` |
| `et al.` | `and others` |
| `vs.` | `compared to` or `against` |
| `viz.` | `specifically` |
| `N.B.` | `Note:` |

### STE-19: No Ambiguous Pronouns

Do not use pronouns (`it`, `this`, `that`, `they`, `them`) when the referent
is unclear. Repeat the noun.

| Non-STE (ambiguous) | STE (explicit) |
| :--- | :--- |
| The worker processes it and sends it to the queue. | The worker processes the event and sends the event to the queue. |
| This prevents race conditions. | Bounded retry prevents race conditions. |

### STE-20: No Dangling Modifiers

Place modifiers directly next to the word they modify.

| Non-STE | STE |
| :--- | :--- |
| Running in production, the bug caused data loss. | The bug caused data loss when the system ran in production. |

---

## Part 8: Code Comment Standards

### Go

```go
// Package auth provides tenant-isolated authorization checks.
package auth

// ValidateToken verifies the JWT signature and extracts user metadata.
// It returns an error if the token is expired or malformed.
func ValidateToken(ctx context.Context, tokenStr string) (*Claims, error) {
    // Read the public key from the secret store.
    // Verify the signature against the token header.
    // Extract the tenant scope from the validated claims.
}
```

**Checklist for Go comments**:

- Start package comments with `Package <name>`.
- Start function comments with the function name.
- Keep each comment line to 20 words or fewer.
- Use imperative form for inline comments.

### Rust

```rust
/// Calculate the checksum for a contiguous byte buffer.
///
/// Return the 32-bit hash value.
///
/// # Errors
///
/// Return `ChecksumError` if the buffer is empty.
pub fn calculate_checksum(buffer: &[u8]) -> Result<u32, ChecksumError> {
    // Process the buffer in 64-byte chunks.
    // Accumulate the running hash with each chunk.
}
```

**Checklist for Rust comments**:

- Start with a single-sentence summary in imperative form.
- Document errors under a `# Errors` heading.
- Document panics under a `# Panics` heading.
- Keep each comment line to 20 words or fewer.

### TypeScript

```typescript
/**
 * Transmit a runtime envelope over the WebSocket connection.
 *
 * @param envelope - The payload to send to the server.
 * @returns A promise that resolves when the transmission completes.
 */
export async function sendEnvelope(envelope: RuntimeEnvelope): Promise<void> {
  // Verify that the connection status is active.
  // Serialize the envelope into binary format.
}
```

**Checklist for TypeScript comments**:

- Start with a single-sentence summary in imperative form.
- Use `@param` and `@returns` tags.
- Keep each comment line to 20 words or fewer.

---

## Part 9: Foundation-Specific Keyword Mappings

This section maps STE rules to specific Foundation domains. Each mapping shows
common violations and their corrected forms.

### Events and Lifecycle

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Event contract | Events should follow the `domain:action:state` pattern, etc. | Events follow the `domain:action:state` pattern: `requested`, `success`, and `failed`. |
| Correlation | The correlation ID should be checked to make sure it's propagated. | Verify that the correlation ID propagates through all lanes. |
| Bookend events | Bookend events are published when the transfer lifecycle starts and ends, etc. | Publish a bookend event when the transfer starts. Publish a second bookend event when the transfer ends. |

### Errors and Fault Tolerance

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Error wrapping | Don't skip error wrapping because you'll lose the stack trace. | Do not skip error wrapping. Error wrapping preserves the stack trace. |
| Circuit breaker | The circuit breaker is something that stops calling a dependency that's failing. | The circuit breaker stops calls to a failing dependency after a threshold. |
| Degradation | When things go wrong, the system should fall back gracefully. | When a dependency fails, the system falls back to a degraded mode. |

### Hermes and Projections

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Projection cache | Hermes is a stale-read projection and it doesn't do writes. | Hermes is a read-only projection cache. Do not use Hermes for writes. |
| Staleness | If it's getting old, schedule a refresh in the background. | When the projection exceeds the staleness threshold, schedule a background refresh. |
| Rebuild | Projections can be rebuilt from the event log, etc. | Rebuild projections from the event log when data becomes inconsistent. |

### Database and Migrations

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Schema evolution | Schema evolution (e.g., adding a large JSONB column) doesn't accidentally degrade read paths. | Schema changes (for example, adding a large JSONB column) must not degrade read paths. |
| Connection pool | Don't run CPU-bound operations in SQL because it exhausts the pool. | Do not run CPU-bound operations in SQL queries. CPU-bound SQL exhausts the connection pool. |
| Migration safety | Migrations should be reversible, etc. | Make every migration reversible. Test the rollback before you merge. |

### Security and Authorization

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Tenant isolation | You shouldn't trust client-supplied org IDs. | Do not trust client-supplied organization identifiers. Derive the organization from the authenticated context. |
| Secret handling | Don't store secrets in the frontend. | Do not store secrets in frontend code. Use environment-injected configuration. |
| Auth context | Tenant scope is derived from auth context and shouldn't change. | Derive the tenant scope from the authenticated context. The tenant scope must not change between request and terminal event. |

### Workers and Background Jobs

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Bounded work | All loops/retries must be bounded, i.e. they need explicit limits. | All loops and retries must have explicit bounds (maximum attempts and deadlines). |
| Dead letter | When retries are exhausted, it goes to the dead-letter queue. | When retries exhaust, move the job to the dead-letter queue. |
| Job composition | Worker chains let you carry out multi-step workflows. | Worker chains execute bounded multi-step workflows. |

### Runtime, WASM, and Transport

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Control buffer | The 4KB control buffer is what's used for high-speed JS/Rust communication. | The 4KB control buffer provides high-speed communication between JS and Rust. |
| Envelope | Every message (HTTP, WS, Redis) is wrapped in a RuntimeEnvelope, etc. | Every message is wrapped in a RuntimeEnvelope. This applies to HTTP, WebSocket, and Redis messages. |
| Lane fallback | When WASM isn't available, it falls back to JSON. | When WASM is not available, the system falls back to JSON transport. |

### Frontend and UI

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| UI primitives | Check ui-minimal before creating local components, etc. | Check the ui-minimal package before you create local components. |
| Store state | Don't use MutationObserver for business state. | Do not use MutationObserver for business state. Use Zustand stores. |
| Command bus | The command bus figures out the route and sends it. | The command bus resolves the route and dispatches the request. |

### Agent Contracts

| Keyword | Non-STE | STE |
| :--- | :--- | :--- |
| Definition of done | Agent-authored changes must carry evidence, i.e. tests and benchmarks. | Agent-authored changes must carry evidence: tests, benchmarks, or review notes. |
| Read order | Read these files if you're changing architecture. | Read these files before you change architecture-sensitive code. |
| Scaffold ownership | Don't edit files marked overwrite in the scaffold manifest. | Do not edit files marked `overwrite` in the scaffold manifest. Foundation owns those files. |

---

## Part 10: Refactoring Checklist

When editing documentation or code comments, verify:

- [ ] All procedural sentences and comments are 20 words or fewer.
- [ ] All descriptive sentences are 25 words or fewer.
- [ ] All paragraphs contain 6 sentences or fewer.
- [ ] No contractions appear (`don't`, `isn't`, `it's`).
- [ ] No Latin abbreviations appear (`e.g.`, `i.e.`, `etc.`).
- [ ] Active voice is used throughout.
- [ ] Noun clusters are 3 words or fewer.
- [ ] No phrasal verbs are used.
- [ ] Safety alerts use `WARNING`, `CAUTION`, or `NOTE` with correct syntax.
- [ ] No semicolons are used.
- [ ] Ambiguous pronouns are replaced with explicit nouns.
- [ ] Conditions appear before instructions, separated by a comma.

---

## Part 11: Exemptions

The following contexts are exempt from strict sentence-length bounds:

1. **Code blocks and shell commands**: Literal code is not natural language.
2. **Table cell content**: Table cells may exceed 25 words when the row
   represents a structured record. Minimize where possible.
3. **Quoted specifications**: Direct quotes from external standards (for
   example, RFC text) are reproduced verbatim.
4. **Commit messages**: Git commit messages follow their own conventions.

The following contexts are exempt from the no-contractions rule:

1. **PHILOSOPHY.md**: This document uses a conversational, narrative voice by
   design. Contractions are permitted to maintain its intended tone.

All other STE rules apply to exempt contexts.

---

## Evidence and Enforcement

- **Practice ID**: CP-37
- **Risk**: Low
- **Automation**: Contextual (future: lint script for word count and
  contraction scanning)
- **Evidence**: Reviewer gate on documentation and comment changes
- **Merge gate**: Contextual (mandatory for practice docs and agent contracts,
  recommended for all other markdown)
