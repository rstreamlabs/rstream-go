# CLI Workflow

This document explains the operational path from authentication to the first forwarded port.

Tunnel creation is documented separately.

## Concepts

### Project

A **project** is managed in rstream. It defines where tunnels live and which engine endpoint is used.
In the CLI, projects are selected using the **project endpoint**.

Projects are typically created and managed from the rstream dashboard.

### Context

A **context** is a local, named configuration used by the CLI at runtime. It defines:
- the engine endpoint to use (data plane)
- the authentication method and credentials

A single machine can store multiple contexts and switch between them.

One context is selected as the **default context**. Most commands use it automatically unless a specific context is selected explicitly.

## Authentication modes

Two modes exist, depending on where the CLI runs.

### Developer machine (account-wide identity)

Appropriate for installations tied to a physical user (laptops, workstations).

- `rstream login` stores an **account-wide token** locally.
- The same token is reused across projects and contexts.
- `rstream project use <project-endpoint>` derives a context from the selected project.
- Switching projects is typically done by running `rstream project use` again.

The account-wide token can be obtained via:
- browser login flow (default)
- token generated from the rstream dashboard
- token generated via the rstream API

All methods lead to the same outcome: an account-wide token stored locally and reused.

### Remote device (project-scoped token)

Appropriate for servers, CI runners, and embedded systems that must not rely on an account-wide identity.

- No `rstream login`.
- A **project-scoped token** is used.
- A context is created explicitly with `--engine` and `--token`.
- Credentials remain narrowly scoped to a single project.

## Developer machine path

### 1) Login

Default behavior: browser login flow, then the resulting account-wide token is stored locally.

```bash
rstream login
```

Alternative token inputs:

```bash
rstream login <token>
rstream login --stdin
rstream login --token-file /path/to/token
```

### 2) Select a project endpoint

List available projects:

```bash
rstream project list
```

Select a project endpoint and set it as default:

```bash
rstream project use <project-endpoint> --default
```

### 3) Forward a local port

`rstream forward 8080` creates a tunnel and forwards incoming traffic to `localhost:8080`.

```bash
rstream forward 8080
```

## Remote device path

### 1) Create a context with a project-scoped token

```bash
rstream context create <name> --engine <host:port> --token <project-scoped-token> --default
```

Alternative token inputs:

```bash
rstream context create <name> --engine <host:port> --stdin --default
rstream context create <name> --engine <host:port> --token-file /path/to/token --default
```

### 2) Forward a local port

```bash
rstream forward 8080
```

## Switching projects and contexts

### Account-wide setups (developer machines)

Fast path: select another project endpoint and set it as default.

```bash
rstream project list
rstream project use <project-endpoint> --default
```

If a specific context name is preferred:

```bash
rstream project use <project-endpoint> --name <context-name> --default
```

### Project-scoped setups (remote devices)

Create a new context (or update an existing one), then set it as default.

```bash
rstream context create <name> --engine <host:port> --token <project-scoped-token>
rstream context use <name>
```

## Context management

List contexts:

```bash
rstream context list
```

Inspect a context:

```bash
rstream context get <name>
```

Set the default context:

```bash
rstream context use <name>
```

Delete a context:

```bash
rstream context delete <name>
```

## Logout

Logout deletes locally stored authentication material for the current rstream environment.

```bash
rstream logout
```

Logout does not delete contexts. Contexts created for remote devices may remain usable if they contain a token, until deleted or updated.

## Summary

- **Projects** are managed in rstream and selected via **project endpoint**.
- **Contexts** are local and define engine endpoint selection and authentication.
- **Developer machine**: `rstream login` then `rstream project use <project-endpoint>`.
- **Remote device**: `rstream context create ... --engine ... --token ...` with a project-scoped token.
- Once a default context exists: `rstream forward <port>` creates a tunnel and forwards traffic to `localhost:<port>`.
