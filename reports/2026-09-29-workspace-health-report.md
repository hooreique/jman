# Workspace health snapshot

## Scope

Non-public workspace diagnostic snapshots and counts are omitted. The guidance
below describes general diagnostic handling, not a particular workspace's health.

## Interpretation

- Main-source errors need owner follow-up because they can affect navigation in
  their caller or compile dependency closure.
- Test errors are advisory for main-source navigation but can limit test queries.
- Unknown markers require build/source-root mapping investigation; they are
  retained in doctor output rather than discarded.

## Separate follow-up

When unknown markers occur, investigate source-root mapping and stale generated
outputs with the workspace owners. This report does not change any workspace.
