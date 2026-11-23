package tests

import (
	"bank-management/api"
	"bank-management/repository"
	"bank-management/services"
	"bank-management/utils"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestParseMoney(t *testing.T) {
	for _, tc := range []struct {
		in      string
		cents   int64
		invalid bool
	}{
		{"0.29", 29, false}, {"123.4", 12340, false}, {"90000000000.00", 9_000_000_000_000, false},
		{"90000000000.01", 0, true}, {"-0.01", 0, true}, {"0.001", 0, true}, {"1e3", 0, true},
	} {
		value, err := services.ReadMoney(json.RawMessage(tc.in), nil)
		if value != tc.cents || (err != nil) != tc.invalid {
			t.Errorf("%s: %d, %v", tc.in, value, err)
		}
	}
}

type requestClient struct {
	router *gin.Engine
	token  string
}

func (h requestClient) request(method, path, key string, data any) *httptest.ResponseRecorder {
	var buffer bytes.Buffer
	if data != nil {
		if err := json.NewEncoder(&buffer).Encode(data); err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequest(method, path, &buffer)
	req.Header.Set("Content-Type", "application/json")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func expect(t *testing.T, result *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if result.Code != status {
		t.Fatalf("HTTP %d, esperado %d: %s", result.Code, status, result.Body)
	}
	var response map[string]any
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func postgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("configure TEST_DATABASE_URL para executar os cenários com PostgreSQL")
	}
	t.Setenv("DATABASE_URL", dsn)
	db, err := repository.ConnectDB()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return db
}

func TestPostgresRoutes(t *testing.T) {
	db := postgres(t)
	if err := repository.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	tokens, err := utils.NewTokens(strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api.SetupRoutes(router, db, tokens, true)
	guest := requestClient{router: router}
	expect(t, guest.request("GET", "/healthz", "", nil), 200)
	expect(t, guest.request("GET", "/auth/overview", "", nil), 401)

	makeClient := func(name string) requestClient {
		email := name + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.test"
		password := "senha-especifica-para-ci"
		body := map[string]string{"email": email, "password": password, "name": name}
		registered := guest.request("POST", "/users", "", body)
		result := expect(t, registered, 201)
		if _, exists := result["password"]; exists {
			t.Fatal("senha retornada no cadastro")
		}
		login := guest.request("POST", "/login", "", body)
		result = expect(t, login, 200)
		cookie := login.Header().Get("Set-Cookie")
		if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "SameSite=Strict") {
			t.Fatal("atributos do cookie incompletos")
		}
		return requestClient{router: router, token: result["token"].(string)}
	}

	alice, bob := makeClient("alice"), makeClient("bob")
	create := func(h requestClient, name string, value int) uint {
		data := expect(t, h.request("POST", "/auth/accounts", "", map[string]any{"name": name, "balance_cents": value}), 201)
		return uint(data["ID"].(float64))
	}
	from, to, foreign := create(alice, "Caixa", 10_000), create(alice, "Reserva", 0), create(bob, "Privada", 500)
	path := "/auth/accounts/transfer"
	body := map[string]any{"from_account_id": from, "to_account_id": to, "amount_cents": 3_000}

	first := expect(t, alice.request("POST", path, "same", body), 200)
	repeat := expect(t, alice.request("POST", path, "same", body), 200)
	if repeat["replayed"] != true || first["movement"].(map[string]any)["id"] != repeat["movement"].(map[string]any)["id"] {
		t.Fatal("o reenvio deveria devolver o lançamento original")
	}
	changed := map[string]any{"from_account_id": from, "to_account_id": to, "amount_cents": 4_000}
	expect(t, alice.request("POST", path, "same", changed), 409)
	expect(t, alice.request("POST", path, "", map[string]any{"from_account_id": from, "to_account_id": foreign, "amount_cents": 100}), 404)

	var group sync.WaitGroup
	codes := make(chan int, 2)
	for _, key := range []string{"parallel-a", "parallel-b"} {
		group.Add(1)
		go func(key string) {
			defer group.Done()
			codes <- alice.request("POST", path, key, changed).Code
		}(key)
	}
	group.Wait()
	firstCode, secondCode := <-codes, <-codes
	if !(firstCode == 200 && secondCode == 400 || firstCode == 400 && secondCode == 200) {
		t.Fatalf("esperados HTTP 200 e 400: %d, %d", firstCode, secondCode)
	}
	data := expect(t, alice.request("GET", "/auth/overview", "", nil), 200)
	accounts := data["accounts"].([]any)
	if len(accounts) != 2 || accounts[0].(map[string]any)["balance_cents"] != float64(3_000) ||
		accounts[1].(map[string]any)["balance_cents"] != float64(7_000) || len(data["movements"].([]any)) != 3 {
		t.Fatalf("saldos e extrato divergentes: %v", data)
	}
	other := expect(t, bob.request("GET", "/auth/overview", "", nil), 200)
	if len(other["accounts"].([]any)) != 1 || len(other["movements"].([]any)) != 1 {
		t.Fatalf("extrato incorreto para outro usuário: %v", other)
	}
	entries := "/auth/accounts/entries"
	expect(t, alice.request("POST", entries, "deposit", map[string]any{"to_account_id": from, "amount": 0.29}), 200)
	expect(t, alice.request("POST", entries, "withdraw", map[string]any{"from_account_id": from, "amount_cents": 1}), 200)
	data = expect(t, alice.request(http.MethodGet, "/auth/overview", "", nil), 200)
	if data["accounts"].([]any)[0].(map[string]any)["balance_cents"] != float64(3_028) {
		t.Fatal("entrada e saída em centavos divergentes")
	}
}

func TestLegacyBalanceMigration(t *testing.T) {
	db := postgres(t)
	schema := "legacy_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() { db.Exec("DROP SCHEMA " + schema + " CASCADE") }()

	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	params := dsn.Query()
	params.Set("search_path", schema)
	dsn.RawQuery = params.Encode()
	t.Setenv("DATABASE_URL", dsn.String())
	legacy, err := repository.ConnectDB()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := legacy.Exec(`CREATE TABLE accounts (
		id bigserial PRIMARY KEY,
		created_at timestamptz,
		updated_at timestamptz,
		deleted_at timestamptz,
		user_id bigint,
		balance double precision
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec("INSERT INTO accounts (user_id, balance) VALUES (1, 19.995)").Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repository.Migrate(context.Background(), legacy); err != nil {
			t.Fatal(err)
		}
	}
	var row struct {
		Balance      float64
		BalanceCents int64
		Name         string
	}
	if err := legacy.Table("accounts").First(&row, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if row.Balance != 19.995 || row.BalanceCents != 2000 || row.Name != "Conta" {
		t.Fatalf("migração incorreta: %+v", row)
	}
}
