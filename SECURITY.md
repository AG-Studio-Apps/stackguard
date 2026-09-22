# Security

stackGuard reads a container engine's socket, which is host access, so a flaw
here matters. If you find one, email **security@ag-applications.com** with
what you found and how to reproduce it, and we will answer within five working
days. Please do not open a public issue for something exploitable until we
have had a chance to fix it.

Things that are by design and not vulnerabilities on their own:

- the container runs as root (see the Dockerfile and README: every capability
  is dropped and the socket is read-only; root buys only opening the socket);
- the relay address, host id, token and payload key arrive in the
  environment; the app writes them at deploy, and they are visible to anyone
  who can inspect the container, which is anyone who can read the socket.

The latest release is the only supported one.
