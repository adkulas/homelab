# Encrypt home network address inventory

The home Site Inventory will version Host IP addresses and device MAC addresses as SOPS-encrypted values, referenced by semantic IDs from readable declarations. This preserves reproducibility without exposing the household's device inventory in plaintext Git history, at the cost of less readable diffs and a required decryption key for validation and deployment. Credentials remain in separate SOPS documents owned by the consuming Stack.
