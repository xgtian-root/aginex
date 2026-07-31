package migrate

import "testing"

func TestPostgresMigrationsUseSessionLock(t *testing.T) {
	locker, err := migrationSessionLocker("postgres")
	if err != nil {
		t.Fatal(err)
	}
	if locker == nil {
		t.Fatal("postgres migrations must use a cross-process session lock")
	}
}

func TestOtherMigrationDriversDoNotClaimPostgresLocking(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			locker, err := migrationSessionLocker(driver)
			if err != nil {
				t.Fatal(err)
			}
			if locker != nil {
				t.Fatalf("%s unexpectedly uses the PostgreSQL session locker", driver)
			}
		})
	}
}
