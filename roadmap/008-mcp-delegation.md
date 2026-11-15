# Restrict delegated MCP access

Publishing the CLI MCP surface currently exposes its complete tool set. Less-trusted consumers need server-enforced tool subsets; behavior annotations and client allow-lists do not restrict calls made with the published credential.

Token and TURN credential tools also return secrets in MCP results. Add short-lived delivery handles and an explicitly approved reveal path before delegating those tools to browser agents. Define both controls together, with expiry and redaction tests, while retaining the existing authorization checks.
