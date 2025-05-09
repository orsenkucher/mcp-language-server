# MCP Language Server

A Model Context Protocol (MCP) server that runs a language server and provides tools for communicating with it.

## Motivation

Claude desktop with the [filesystem](https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem) server feels like magic when working on small projects. This starts to fall apart after you add a few files and imports. With this project, I want to create that experience when working with large projects.

Language servers excel at tasks that LLMs often struggle with, such as precisely understanding types, understanding relationships, and providing accurate symbol references. This project aims to makes bring those tools to LLMs. LSP also seems like a clear inspiration for MCP so why not jam them together?

## Status

⚠️ Pre-beta Quality ⚠️

I have tested this server with the following language servers

- pyright (Python)
- tsserver (TypeScript)
- gopls (Go)
- rust-analyzer (Rust)

But it should be compatible with many more.

## Tools

- `read_definition`: Retrieves the complete source code definition of any symbol (function, type, constant, etc.) from your codebase.
- `find_references`: Locates all usages and references of a symbol throughout the codebase.
- `get_diagnostics`: Provides diagnostic information for a specific file, including warnings and errors.
- `get_codelens`: Retrieves code lens hints for additional context and actions on your code.
- `execute_codelens`: Runs a code lens action.
- `apply_text_edit`: Allows making multiple text edits to a file programmatically.

Behind the scenes, this MCP server can act on `workspace/applyEdit` requests from the language server, so it can apply things like refactor requests, adding imports, formatting code, etc.

Each tool supports various options for customizing output, such as including line numbers or additional context. See the tool documentation for detailed usage. Line numbers are necessary for `apply_text_edit` to be able to make accurate edits.

## About

This codebase makes use of edited code from [gopls](https://go.googlesource.com/tools/+/refs/heads/master/gopls/internal/protocol) to handle LSP communication. See ATTRIBUTION for details.

[mcp-golang](https://github.com/metoro-io/mcp-golang) is used for MCP communication.

## Prerequisites

Install Go: Follow instructions at <https://golang.org/doc/install>

Fetch or update this server:

```bash
go install github.com/isaacphi/mcp-language-server@latest
```

Install a language server for your codebase:

- Python (pyright): `npm install -g pyright`
- TypeScript (tsserver): `npm install -g typescript typescript-language-server`
- Go (gopls): `go install golang.org/x/tools/gopls@latest`
- Rust (rust-analyzer): `rustup component add rust-analyzer`
- Or use any language server

## Setup

Add something like the following configuration to your Claude Desktop settings (or similar MCP-enabled client):

```json
{
  "mcpServers": {
    "language-server": {
      "command": "go",
      "args": [
        "run",
        "github.com/isaacphi/mcp-language-server@latest",
        "--workspace",
        "/Users/you/dev/yourcodebase",
        "--lsp",
        "/opt/homebrew/bin/pyright-langserver",
        "--",
        "--stdio"
      ],
      "env": {
        "DEBUG": "1"
      }
    }
  }
}
```

Replace:

- `/Users/you/dev/yourcodebase` with the absolute path to your project
- `/opt/homebrew/bin/pyright-langserver` with the path to your language server (found using `which` command e.g. `which pyright-langserver`)
- Any aruments after `--` are sent as arguments to your language server.
- Any env variables are passed on to the language server. Some may be necessary for you language server. For example, `gopls` required `GOPATH` and `GOCACHE` in order for me to get it working properly.
- `DEBUG=1` is optional. See below.

## Development

Clone the repository:

```bash
git clone https://github.com/isaacphi/mcp-language-server.git
cd mcp-language-server
```

Install dev dependencies:

```bash
go mod download
```

Build:

```bash
go build
```

Configure your Claude Desktop (or similar) to use the local binary:

```json
{
  "mcpServers": {
    "language-server": {
      "command": "/full/path/to/your/clone/mcp-language-server/mcp-language-server",
      "args": [
        "--workspace",
        "/path/to/workspace",
        "--lsp",
        "/path/to/language/server"
      ],
      "env": {
        "DEBUG": "1"
      }
    }
  }
}
```

Rebuild after making changes.

## Feedback

Include

```
env: {
  "DEBUG": 1
}
```

To get detailed LSP and application logs. Please include as much information as possible when opening issues.

The following features are on my radar:

- [x] Read definition
- [x] Get references
- [x] Apply edit
- [x] Get diagnostics
- [x] Code lens
- [ ] Hover info
- [ ] Code actions
- [ ] Better handling of context and cancellation
- [ ] Add LSP server configuration options and presets for common languages
- [ ] Make a more consistent and scalable API for tools (pagination, etc.)
- [ ] Create tools at a higher level of abstraction, combining diagnostics, code lens, hover, and code actions when reading definitions or references.
