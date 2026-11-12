package database

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenMySQLAndClose(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set")
	}
	database, err := OpenMySQL(context.Background(), dsn)
	require.NoError(t, err)
	require.NotNil(t, database.GORM)
	require.NotNil(t, database.SQL)
	require.NoError(t, database.SQL.Ping())
	require.NoError(t, database.Close())
}

func TestOpenMySQLRejectsInvalidDSN(t *testing.T) {
	database, err := OpenMySQL(context.Background(), "not a valid mysql dsn")
	require.Nil(t, database)
	require.ErrorContains(t, err, "open mysql")
}

func TestCloseAllowsMissingDatabase(t *testing.T) {
	var nilDatabase *MySQL
	require.NoError(t, nilDatabase.Close())
	require.NoError(t, (&MySQL{}).Close())
}
