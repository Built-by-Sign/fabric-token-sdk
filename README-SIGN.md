# EthSign fork of fabric-token-sdk

This is EthSign's fork of [hyperledger-labs/fabric-token-sdk](https://github.com/hyperledger-labs/fabric-token-sdk), maintained so we can cherry-pick upstream fixes and add CBDC-specific adaptations ahead of upstream release cycles.

The fork exists solely to support the CBDC chain project. It is not a general-purpose distribution of the SDK.

## Relationship to upstream

- Default branch: `main` tracks upstream verbatim (no EthSign commits).
- EthSign branches are named `sign/<upstream-baseline>` and carry EthSign patches on top of a specific upstream tag.
- Every EthSign patch is expected to be upstreamed or deleted once the equivalent lands in an upstream release.

## Branches

| Branch | Baseline | Purpose |
|--------|----------|---------|
| `main` | upstream `main` | Tracks upstream; never carries EthSign patches directly |
| `sign/v0.8.1` | `v0.8.1` | Active branch consumed by cbdc-chain. Adds backported upstream fixes that have not yet shipped in a tagged release |

## Consumption

cbdc-chain consumes this fork via a `replace` directive in `cbdc-biz/go.mod`:

```
replace github.com/hyperledger-labs/fabric-token-sdk => github.com/EthSign/fabric-token-sdk <pseudo-version>
```

The internal Go module path is unchanged (`github.com/hyperledger-labs/fabric-token-sdk`), so imports in cbdc-biz do not need to be rewritten.

## Adding a patch

1. Branch off the appropriate `sign/*` branch.
2. For upstream backports, cherry-pick by merge SHA and keep the original commit message.
3. For EthSign-specific changes, prefix the commit subject with `[sign]` so it's visible in `git log`.
4. Open a PR into the `sign/*` branch. Keep EthSign changes minimal and documented — every patch is a rebase cost later.

## Deleting the fork

When upstream ships a release that supersedes every patch on `sign/<baseline>`, bump cbdc-biz to the upstream release, remove the `replace` directive, and archive the branch. The fork itself can remain in place as a historical record but should not accumulate new work.

## Ownership

| Role | Owner |
|------|-------|
| Rebase cadence | ad-hoc, driven by cbdc-chain needs |
| Security watch | upstream CVEs do not auto-flow; track via upstream release notes |
| CODEOWNERS | TODO — set before any third-party contributor touches the fork |

## Current patches on `sign/v0.8.1`

| Upstream ref | Reason | Delete when |
|--------------|--------|-------------|
| [PR #1500](https://github.com/hyperledger-labs/fabric-token-sdk/pull/1500) | Adds index on `owner_wallet_id` and `status`; needed by cbdc-biz UTXO maintenance detector | upstream release > v0.10.0 containing PR #1500 is adopted |
