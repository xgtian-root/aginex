import type { SetupDatabaseTest } from "./api";

type DatabaseInput = SetupDatabaseTest["database"];

function acceptDatabaseInput(input: DatabaseInput) {
  return input;
}

acceptDatabaseInput({
  driver: "sqlite",
  sqlite: { directory: "data", filename: "aginex.db" },
});
acceptDatabaseInput({
  driver: "postgres",
  postgres: {
    host: "localhost",
    port: 5432,
    database: "aginex",
    username: "aginex",
    password: "secret",
    sslMode: "require",
  },
});
acceptDatabaseInput({
  driver: "mysql",
  mysql: {
    host: "localhost",
    port: 3306,
    database: "aginex",
    username: "aginex",
    password: "secret",
    tlsMode: "required",
  },
});

// @ts-expect-error The selected driver's nested object is required.
acceptDatabaseInput({ driver: "postgres" });

acceptDatabaseInput({
  driver: "sqlite",
  // @ts-expect-error Driver and nested object must describe the same database.
  postgres: {
    host: "localhost",
    port: 5432,
    database: "aginex",
    username: "aginex",
    password: "secret",
    sslMode: "require",
  },
});

acceptDatabaseInput({
  driver: "sqlite",
  sqlite: { directory: "data", filename: "aginex.db" },
  // @ts-expect-error Multiple driver-specific objects are not accepted.
  mysql: {
    host: "localhost",
    port: 3306,
    database: "aginex",
    username: "aginex",
    password: "secret",
    tlsMode: "required",
  },
});
