package target

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"leafygo/internal/domain"
)

var validSSLModes = map[string]struct{}{
	"disable":     {},
	"allow":       {},
	"prefer":      {},
	"require":     {},
	"verify-ca":   {},
	"verify-full": {},
}

// BuildDSN constructs a PostgreSQL connection string for the target connection.
func BuildDSN(db domain.DBConnection, password string) (string, error) {
	return buildDSNImpl(db, password)
}

func buildDSN(db domain.DBConnection, password string) (string, error) {
	return buildDSNImpl(db, password)
}

func buildDSNImpl(db domain.DBConnection, password string) (string, error) {
	sslMode := strings.TrimSpace(db.SSLMode)
	if _, ok := validSSLModes[sslMode]; !ok {
		return "", fmt.Errorf("invalid sslmode %q: must be one of disable, allow, prefer, require, verify-ca, verify-full", db.SSLMode)
	}

	host := strings.TrimSpace(db.Host)
	if host == "" {
		return "", errors.New("host cannot be empty")
	}

	// Strip IPv6 square brackets if caller supplied them, so net.JoinHostPort handles it cleanly
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	port := db.Port
	if port <= 0 {
		port = 5432
	}

	hostPort := net.JoinHostPort(host, strconv.Itoa(port))

	dbName := strings.TrimSpace(db.DBName)
	if dbName == "" {
		return "", errors.New("database name cannot be empty")
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(db.Username, password),
		Host:   hostPort,
		Path:   "/" + strings.TrimPrefix(dbName, "/"),
	}

	q := u.Query()
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()

	return u.String(), nil
}
