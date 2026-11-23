package main

import (
	"bank-management/api"
	"bank-management/repository"
	"bank-management/utils"
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed web/*
var website embed.FS

func main() {
	tokens, err := utils.NewTokens(os.Getenv("JWT_SECRET"))
	if err != nil {
		log.Fatal(err)
	}
	db, err := repository.ConnectDB()
	if err != nil {
		log.Fatal("Não foi possível conectar ao PostgreSQL.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = repository.Migrate(ctx, db)
	cancel()
	if err != nil {
		log.Fatal("Migração do PostgreSQL falhou.")
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.SetTrustedProxies(nil)
	api.SetupRoutes(router, db, tokens, os.Getenv("COOKIE_SECURE") == "1")
	assets, err := fs.Sub(website, "web")
	if err != nil {
		log.Fatal(err)
	}
	router.StaticFS("/assets", http.FS(assets))
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		log.Fatal(err)
	}
	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{
		Addr: ":" + port, Handler: router, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("Servidor HTTP na porta %s", port)
	log.Fatal(server.ListenAndServe())
}
