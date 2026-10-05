# ssh-chat

An old-school BBS-style chat server you reach with plain `ssh`. No accounts and
no passwords: connect and you are in.

```
ssh -p 2222 your-server
```

- Multiple channels, private messages, locks, invites, bans and operators
- Your SSH key is your identity: same key, same name, every time
- **SysOp-Gus**, a built-in rules-based bot (trivia, offline mail, MOTD). No AI, no network calls
- One static Go binary with SQLite. Tested with 1000 users on a single CPU core

## Run it

```
podman build -t ssh-chat --build-arg VERSION=0.9.5 .
podman run -d --name ssh-chat -p 2222:2222 -v ssh-chat-data:/data ssh-chat
```

Docker works the same way. With compose: `cd deploy && podman-compose up -d --build`.

Never delete the data volume. It holds the database and the server's SSH host
key, and losing the host key breaks every client's trust in the server.

## Configure it

Every setting is an environment variable. Copy [`.env.example`](.env.example),
which lists them all with their defaults, and pass it with `--env-file`, compose
`env_file:` or a quadlet `EnvironmentFile=`.

To make yourself an administrator, add your key's fingerprint:

```
ssh-keygen -lf ~/.ssh/id_ed25519.pub        # prints SHA256:...
SSHCHAT_ADMIN_FPS=SHA256:...
```

## Use it

Inside the chat, type `/help` for commands, `/gus` for the bot's commands and
`/keys` for keyboard shortcuts. New users without an SSH key can type `!ssh` for
a step-by-step guide to making one.

A terminal with a black background, 256 colours and UTF-8 looks best.

## Build from source

Needs Go 1.24.

```
go build ./cmd/server
go test ./...
```

[`STATUS.md`](STATUS.md) covers the design, every command and setting, the limits
and the load-test results.
