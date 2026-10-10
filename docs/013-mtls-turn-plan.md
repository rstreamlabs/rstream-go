# Engine HTTP mTLS and managed TURN delivery plan

The accepted scope is certificate-only Engine HTTP access and TURN renewal from
reusable SDK clients, preserving existing device configuration and ordinary
browser behavior. The implementation contract is documented in
[Engine mTLS and TURN](014-engine-mtls-turn.md).

## Source responsibilities

| Repository | Deliverable |
| --- | --- |
| Engine | Explicit ordinary/dedicated origin mapping, anonymous discovery, project-bound HTTP certificate admission on TCP/QUIC, managed TURN issuer and OpenAPI |
| TURN | Realm-scoped issued-v2 verification, original identity/permission revalidation, retained v1 compatibility |
| Go | Owned discovery cache/transports, local PAT/APP and managed issuance, bounded availability fallback, CLI inventory/watch/UI/doctor consumers |
| JavaScript | Aligned TURN selection, Node certificate discovery/HTTPS/SSE, runtime lifecycle and published package changesets |
| Python | Certificate discovery for HTTP inventory/SSE/WebSocket and owned cancellation |
| Next.js | Explicit Engine/TURN certificate permissions, legacy grant preservation, matching managed issuance contract, product/internal documentation |
| Examples | Reusable SDK client for TURN renewal, compatible SDK pins and preserved video dependencies |

Java and C++ have no equivalent managed TURN helper or Engine HTTP certificate
surface to modify in this change. Their native transport support is unchanged.
Node and Python HTTP adapters use TCP; Go and Engine also exercise HTTP/3.

## Compatibility requirements

- SDKs discover an explicitly configured URL from the ordinary Engine origin;
  they never derive a hostname pattern. Cache discovery in memory per client,
  with bounded concurrent work, cancellation and deterministic closure.
- Discovery verifies server TLS and sends no client certificate, signer callback,
  bearer token or cookie. Ordinary browser origins never request a certificate.
- Dedicated Engine HTTP admission accepts the enrolled certificate or a token,
  rejects mixed identities and binds certificate admission to project, SNI and
  authority. Existing permissions, policy and expiry checks still apply.
- Automatic TURN mode derives locally for eligible PAT/APP identities and known
  serving metadata; otherwise it uses the configured Control plane, with bounded
  Engine fallback only for availability failures. Certificates use Engine directly.
- Both managed issuers accept the same optional bounded TTL request, return the
  same response shape, require `turn.credentials.create`, and clip lifetime to
  the authenticated identity. Local derivation requires `turn.relay.allocate`.
- Existing certificate grants are preserved. Permission-policy migration changes
  the default for new policies and converts old policies without widening rights.
- CLI operations that depend only on Engine become available under their existing
  rights. Optional Control plane enrichment cannot borrow an administrator token.
  Control-plane-only administration remains outside certificate access.

## Validation and release gates

1. Integrate onto current main in every affected repository, retaining existing
   Doctor and persistent WebTTY custom-domain corrections. Require repository CI
   at the exact candidate revision.
2. Validate the shared PAT/APP/issued-v2 vectors and real database authorization,
   TLS H1/H2/H3, ECH, SSE/WebSocket, lifecycle/race behavior, restricted permissions,
   identity expiry, project scope and actual TURN allocation/relay traffic.
3. Validate topology/schema/inventory, OpenTofu plans and mocks, Ansible mounts,
   keyring continuity and no change to existing application/WireGuard/ECH keys.
4. Publish immutable candidates through official workflows. Freeze the exact
   cross-repository WebTTY assembly and consumer lock; complete the assembled
   certification before publication. Update consumers with the published SDKs.
5. Prepare new private deployment inputs from current authorities. Add only the
   explicit mTLS DNS/certificate mappings and new durable TURN signing material.
   Deploy v2-capable relay verification before enabling either v2 issuer.
6. Deploy and qualify staging: feature matrix plus project settings, persistent
   WebTTY domains, authentication/challenge, E2E/routing, CLI/MCP/dashboard, usage,
   host/observability health and rollback/recovery. Prior staging evidence is a
   regression reference, not acceptance of this candidate.
7. Promote exactly the accepted artifacts to production using the maintained
   capacity-specific runbook, regional draining and observation. Record versions,
   workflows, digests, validation and rollback authority privately. Synchronize
   source checkouts and remove temporary identities/resources after acceptance.

Staging and production promotion are authorized after these gates pass. The
maintained Engine deployment runbooks govern execution; live identities and
acceptance evidence belong in the private operator state, not in this plan.
