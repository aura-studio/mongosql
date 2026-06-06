// Command mysql-simulator listens on the MySQL wire protocol and routes
// queries through the SQL→MongoDB driver. Point any MySQL UI client
// (MySQL Workbench, DBeaver, TablePlus, Sequel Ace, the mysql CLI, …)
// at it and execute SQL against MongoDB collections.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	gomysql "github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"

	"github.com/aura-studio/mongosql/driver"
)

func main() {
	var (
		listen   = flag.String("listen", "127.0.0.1:3306", "MySQL wire protocol listen address")
		mongoURI = flag.String("mongo-uri", "mongodb://localhost:27017/sqlmongo", "MongoDB connection URI; the database to expose as the current schema is taken from its path")
		user     = flag.String("user", "root", "expected MySQL username")
		password = flag.String("password", "", "expected MySQL password (empty disables password check)")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	d, err := driver.Connect(ctx, *mongoURI)
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer d.Close(context.Background())
	dbName := d.DB().Name()
	log.Printf("connected to mongo %s, default db=%s", *mongoURI, dbName)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen %s: %v", *listen, err)
	}
	defer ln.Close()
	log.Printf("mysql-simulator listening on %s (user=%q)", *listen, *user)

	// Generate an RSA key pair for caching_sha2_password in non-TLS mode.
	// MySQL 8+ / 9 clients can encrypt the password with the server's RSA
	// public key (--get-server-public-key behaviour) which avoids the need
	// for TLS while still supporting modern auth plugins.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("generate RSA key: %v", err)
	}
	// caching_sha2_password with an RSA key but no TLS: clients may request
	// the public key to encrypt the password over a plain-text connection.
	srv := server.NewServer("8.0.36", gomysql.DEFAULT_COLLATION_ID, gomysql.AUTH_CACHING_SHA2_PASSWORD, rsaKey, nil)

	// Track active client connections so we can close them on shutdown.
	var mu sync.Mutex
	activeConns := make(map[net.Conn]struct{})

	go func() {
		<-ctx.Done()
		log.Printf("shutting down")
		ln.Close()
		// Close all active client connections so HandleCommand unblocks.
		mu.Lock()
		for c := range activeConns {
			c.Close()
		}
		mu.Unlock()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("accept: %v", err)
			continue
		}
		mu.Lock()
		activeConns[conn] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer func() {
				c.Close()
				mu.Lock()
				delete(activeConns, c)
				mu.Unlock()
			}()
			handleConn(srv, c, *user, *password, d, dbName)
		}(conn)
	}
	wg.Wait()
	os.Exit(0)
}

func handleConn(srv *server.Server, c net.Conn, user, password string, d *driver.Driver, defaultDB string) {
	h := newHandler(d, defaultDB)
	authHandler := server.NewInMemoryAuthenticationHandler(gomysql.AUTH_CACHING_SHA2_PASSWORD)
	if err := authHandler.AddUser(user, password, gomysql.AUTH_CACHING_SHA2_PASSWORD); err != nil {
		log.Printf("auth setup from %s: %v", c.RemoteAddr(), err)
		return
	}
	mysqlConn, err := srv.NewCustomizedConn(c, authHandler, h)
	if err != nil {
		log.Printf("handshake from %s: %v", c.RemoteAddr(), err)
		return
	}
	log.Printf("client %s connected (id=%d)", c.RemoteAddr(), mysqlConn.ConnectionID())
	for !mysqlConn.Closed() {
		if err := mysqlConn.HandleCommand(); err != nil {
			log.Printf("client %s: %v", c.RemoteAddr(), err)
			return
		}
	}
}
