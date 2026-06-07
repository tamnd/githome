# Postgres for tests and local development

The store speaks two dialects, SQLite and Postgres. SQLite runs from a temp
file and needs nothing. The Postgres dialect runs only when
`GITHOME_TEST_POSTGRES_DSN` points at a live server. This compose file brings
one up.

It is Postgres 18, the same major version and the same credentials the CI test
matrix uses, so a green Postgres run here matches a green Postgres leg in CI.

## Start it (podman)

```sh
make pg-up
```

That runs `podman compose -f docker/postgres/compose.yaml up -d` and waits for
the health check to pass. The server listens on `localhost:5432` with database
`githome_test`, user `githome`, password `githome`.

To use docker instead, override the engine:

```sh
make pg-up COMPOSE="docker compose"
```

## Run the Postgres tests against it

```sh
make test-postgres
```

That exports the DSN and runs the suite, which then exercises both dialects:

```
GITHOME_TEST_POSTGRES_DSN=postgres://githome:githome@localhost:5432/githome_test?sslmode=disable
```

The tests reset the schema themselves at the start of each case, so the
database can be reused between runs.

## Stop it

```sh
make pg-down        # stop and remove the container, keep the data volume
make pg-down-clean  # also drop the data volume for a clean slate
```
