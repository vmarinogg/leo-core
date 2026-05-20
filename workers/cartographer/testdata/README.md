# My Test Project

A project for testing the cartographer.

## Decision

We chose to use Go for the backend because of its performance characteristics
and strong standard library for HTTP services.

## Pattern

All services follow the repository pattern for data access.

Decision: Use cobra for CLI argument parsing.

Pattern: Errors are always wrapped with fmt.Errorf.

See https://example.com/docs for more information.
Check version v1.2.3 of the library.
