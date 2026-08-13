# Home Media Stack

This context describes the household media libraries and the system that manages them.

## Language

**Movie Library**:
The household's managed collection of movies.
_Avoid_: Film collection

**Series Library**:
The household's managed collection of episodic television series.
_Avoid_: TV shows library, television collection

**Public Torrent Source**:
A publicly accessible torrent index from which media releases may be discovered.
_Avoid_: Public tracker

**Production Environment**:
The live Media Stack that manages the household's libraries and acquisition activity.
_Avoid_: Main stack, live stack

**Staging Environment**:
An isolated but operational Media Stack used to verify deployment, configuration, acquisition, and recovery without affecting
the Production Environment.
_Avoid_: Test stack, dev stack

**Declared Configuration**:
The settings intentionally controlled by this repository and restored to their declared values during reconciliation.
_Avoid_: Managed settings, desired state

**Promotion**:
The deliberate application to the Production Environment of an exact change already verified in the Staging Environment.
_Avoid_: Deploy, release

**Restore Drill**:
A recovery exercise that restores a Production Environment backup into an isolated Staging Environment and verifies the
recovered state.
_Avoid_: Restore test
