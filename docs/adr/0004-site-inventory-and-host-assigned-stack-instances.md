# Put Host-assigned Stack Instances in a Site Inventory

A versioned Site Inventory owns explicit Host and Stack Instance references above independently deployable Stacks. Each Stack
Instance is assigned to one Host and owns its Site- and Host-dependent inputs, while reusable Stack policy remains under the
Stack and Production and Staging remain Environments within the instance. This avoids copied per-Host topology and a single
repository-wide Compose project, at the cost of a typed adapter for each Stack.

Sensitive Site topology belongs to a small Site-specific SOPS document referenced by semantic IDs; credentials stay with the
consuming Stack and runtime state stays outside Git. Repository-wide behavior is read-only validation and planning until this
model has earned a mutation boundary.
