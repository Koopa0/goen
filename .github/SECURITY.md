# Security policy

## Supported versions

Report against `main`. goen is a reference project with no releases; fixes land
on `main` and nothing earlier is maintained.

## Reporting a vulnerability

Report privately through
[GitHub's private advisory form](https://github.com/Koopa0/goen/security/advisories/new),
never a public issue. Attach no customer data, no order numbers and no
credentials.

goen takes money, holds stock and files tax documents, so the things worth
reporting are the ones that reach those:

- a path that moves money, stock or a ledger without going through a
  `SECURITY DEFINER` function, or that the `store` or `admin` role can reach
  directly;
- a Stripe webhook that posts an effect without a verified signature, or one
  that is attributed to an order from the event rather than from goen's own
  payment row;
- an order, address, invoice or account reachable by a browser that did not
  place it or does not own it;
- a form that writes without a `POST`, or a write htmx can perform that a
  scripting-off browser cannot;
- anything that reaches the environment outside `cmd/goen/main.go`, or reads a
  secret from anywhere but the environment.

A report that names the constraint, trigger or test that should have refused
the path is the fastest kind to act on.
