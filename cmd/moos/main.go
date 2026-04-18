package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/kernel"
	"moos/kernel/internal/mcp"
	"moos/kernel/internal/operad"
	"moos/kernel/internal/transport"
)

// tDay computes current T-day: days since T=0 (2025-11-01 00:00 CEST = 2025-10-31 23:00 UTC).
func tDay() int {
	t0 := time.Date(2025, 10, 31, 23, 0, 0, 0, time.UTC)
	return int(time.Now().UTC().Sub(t0).Hours() / 24)
}

func main() {
	ontologyPath := flag.String("ontology", "", "path to ontology.json (ffs0/kb/superset/ontology.json)")
	logPath := flag.String("log", "", "path to JSONL rewrite log (empty = in-memory)")
	listenAddr := flag.String("listen", ":8000", "HTTP transport listen address")
	mcpAddr := flag.String("mcp-addr", ":8080", "MCP server listen address")
	mcpStdio := flag.Bool("mcp-stdio", false, "also run MCP on stdin/stdout")
	stdioOnly := flag.Bool("stdio-only", false, "run MCP stdio only (no HTTP/SSE servers — for Desktop integration)")
	doSeed := flag.Bool("seed", false, "seed infrastructure nodes from flags")
	seedUser := flag.String("seed-user", "sam", "username for seed node")
	seedWS := flag.String("seed-ws", "hp-laptop", "workstation name for seed node")
	quicAddr := flag.String("quic-addr", "", "UDP address for HTTP/3 QUIC listener (e.g. :4433). Requires --tls-cert and --tls-key.")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file (PEM) for QUIC listener")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file (PEM) for QUIC listener")
	sweepInterval := flag.Duration("sweep-interval", 30*time.Second,
		"t_hook sweep tick interval (0 disables; default 30s). Each tick evaluates all pending t_hooks and emits a governance_proposal per firing.")
	flag.Parse()

	// --- Load registry ---
	registry, err := operad.LoadRegistry(*ontologyPath)
	if err != nil {
		log.Fatalf("operad: %v", err)
	}
	if *ontologyPath == "" {
		log.Println("warning: no --ontology path provided, running without type validation")
	} else {
		log.Printf("operad: loaded %d node types, %d rewrite categories from %s",
			len(registry.NodeTypes), len(registry.RewriteCategories), *ontologyPath)
	}

	// --- Open store ---
	var store kernel.Store
	if *logPath != "" {
		ls, err := kernel.NewLogStore(*logPath)
		if err != nil {
			log.Fatalf("store: %v", err)
		}
		store = ls
		log.Printf("store: JSONL log at %s", *logPath)
	} else {
		store = kernel.NewMemStore()
		log.Println("store: in-memory (non-persistent)")
	}

	// --- Create runtime (replays full log) ---
	rt, err := kernel.NewRuntime(store, registry)
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	log.Printf("runtime: replayed %d rewrites", rt.LogLen())

	// --- Seed infrastructure nodes (idempotent) ---
	if *doSeed {
		if err := seedInfrastructure(rt, *seedUser, *seedWS); err != nil {
			log.Fatalf("seed: %v", err)
		}
		log.Printf("seed: infrastructure nodes ready (user=%s, workstation=%s)", *seedUser, *seedWS)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- stdio-only mode: skip HTTP/SSE, serve MCP on stdin/stdout, exit when stdin closes ---
	if *stdioOnly {
		log.Println("mcp: stdio-only mode")
		mcpSrv := mcp.NewServer(rt)
		mcpSrv.HandleStdio(ctx, os.Stdin, os.Stdout)
		return
	}

	// --- Start HTTP transport ---
	tSrv := transport.NewServer(rt, registry, tDay())
	httpSrv := &http.Server{
		Addr:    *listenAddr,
		Handler: tSrv.Handler(),
	}
	go func() {
		log.Printf("transport: listening on %s", *listenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("transport: %v", err)
		}
	}()
	// M9: start twin sync goroutine (forwards rewrites to eager twin_link endpoints).
	go tSrv.RunTwinSync()

	// --- Start MCP server ---
	mcpSrv := mcp.NewServer(rt)
	mcpHTTP := &http.Server{
		Addr:    *mcpAddr,
		Handler: mcpSrv.Handler(),
	}
	go func() {
		log.Printf("mcp: listening on %s", *mcpAddr)
		if err := mcpHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("mcp: %v", err)
		}
	}()

	// --- Optional: MCP over stdin/stdout ---
	if *mcpStdio {
		go func() {
			log.Println("mcp: starting stdio transport")
			mcpSrv.HandleStdio(ctx, os.Stdin, os.Stdout)
		}()
	}

	// --- Start HTTP/3 QUIC transport (M10) ---
	if *quicAddr != "" {
		// Fail fast with a clear message rather than letting ServeQUIC emit a
		// generic "open tls-cert: no such file" deep in the goroutine where
		// it's easy to miss. Addresses PR #8 review (Copilot).
		if *tlsCert == "" || *tlsKey == "" {
			log.Fatalf("--quic-addr=%s requires both --tls-cert and --tls-key to be set (got cert=%q key=%q)",
				*quicAddr, *tlsCert, *tlsKey)
		}
		// Advertise HTTP/3 via Alt-Svc, using the actual configured QUIC
		// UDP port, not a hardcoded :443. Clients need the correct
		// port/authority for HTTP/3 discovery; hardcoded :443 was a bug
		// when running on non-default ports (PR #8 review, Copilot+Gemini).
		tSrv.SetAltSvc(fmt.Sprintf(`h3=%q; ma=2592000`, *quicAddr))
		go tSrv.ServeQUIC(*quicAddr, *tlsCert, *tlsKey)
	}

	// --- Start time-driven t_hook sweep (§M14 hook-predicates, round-9 M2) ---
	// Every --sweep-interval, walk pending t_hooks and ADD a governance_proposal
	// per hook whose predicate fires. Proposals sit at status=pending awaiting
	// admin ratification. --sweep-interval=0 disables.
	//
	// The sweep actor URN defaults to urn:moos:kernel:sweep; when seeding is
	// enabled we override to the specific kernel URN for traceability.
	if *doSeed {
		rt.SetSweepActor(graph.URN(fmt.Sprintf("urn:moos:kernel:%s.primary", *seedWS)))
	}
	go rt.RunTimedSweep(ctx, *sweepInterval)

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()

	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Printf("transport shutdown: %v", err)
	}
	if err := mcpHTTP.Shutdown(shutCtx); err != nil {
		log.Printf("mcp shutdown: %v", err)
	}
	log.Println("stopped")
}

// seedInfrastructure seeds the S2 core nodes (user, workstation, kernel).
// All calls are SeedIfAbsent — safe to call on every restart.
func seedInfrastructure(rt *kernel.Runtime, user, ws string) error {
	actor := graph.URN("urn:moos:user:" + user)
	now := time.Now().UTC().Format(time.RFC3339)

	nodes := []struct {
		env  graph.Envelope
		desc string
	}{
		{
			desc: "user",
			env: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       actor,
				NodeURN:     graph.URN(fmt.Sprintf("urn:moos:user:%s", user)),
				TypeID:      "user",
				Properties: map[string]graph.Property{
					"name":       {Value: user, Mutability: "immutable", StratumOrigin: graph.S2},
					"created_at": {Value: now, Mutability: "immutable", StratumOrigin: graph.S2},
				},
			},
		},
		{
			desc: "workstation",
			env: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       actor,
				NodeURN:     graph.URN(fmt.Sprintf("urn:moos:workstation:%s", ws)),
				TypeID:      "workstation",
				Properties: map[string]graph.Property{
					"hostname": {Value: ws, Mutability: "immutable", StratumOrigin: graph.S2},
					"os":       {Value: detectOS(), Mutability: "immutable", StratumOrigin: graph.S2},
					"arch":     {Value: detectArch(), Mutability: "immutable", StratumOrigin: graph.S2},
				},
			},
		},
		{
			desc: "kernel",
			env: graph.Envelope{
				RewriteType: graph.ADD,
				Actor:       actor,
				NodeURN:     graph.URN(fmt.Sprintf("urn:moos:kernel:%s.primary", ws)),
				TypeID:      "kernel",
				Properties: map[string]graph.Property{
					"version":    {Value: "1.0.0", Mutability: "immutable", StratumOrigin: graph.S2},
					"status":     {Value: "active", Mutability: "mutable", AuthorityScope: "kernel", StratumOrigin: graph.S2},
					"created_at": {Value: now, Mutability: "immutable", StratumOrigin: graph.S2},
				},
			},
		},
	}

	for _, n := range nodes {
		if err := rt.SeedIfAbsent(n.env); err != nil {
			return fmt.Errorf("seed %s: %w", n.desc, err)
		}
	}

	// Seed WF01: user owns workstation
	if err := rt.SeedIfAbsent(graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF01,
		Actor:           actor,
		RelationURN:     graph.URN(fmt.Sprintf("urn:moos:rel:%s.owns.%s", user, ws)),
		SrcURN:          graph.URN(fmt.Sprintf("urn:moos:user:%s", user)),
		SrcPort:         "owns",
		TgtURN:          graph.URN(fmt.Sprintf("urn:moos:workstation:%s", ws)),
		TgtPort:         "child",
	}); err != nil {
		return fmt.Errorf("seed WF01 owns: %w", err)
	}

	// Seed WF03: workstation hosts kernel
	if err := rt.SeedIfAbsent(graph.Envelope{
		RewriteType:     graph.LINK,
		RewriteCategory: graph.WF03,
		Actor:           actor,
		RelationURN:     graph.URN(fmt.Sprintf("urn:moos:rel:%s.hosts.primary", ws)),
		SrcURN:          graph.URN(fmt.Sprintf("urn:moos:workstation:%s", ws)),
		SrcPort:         "hosts",
		TgtURN:          graph.URN(fmt.Sprintf("urn:moos:kernel:%s.primary", ws)),
		TgtPort:         "hosted-on",
	}); err != nil {
		return fmt.Errorf("seed WF03 hosts: %w", err)
	}

	return nil
}

func detectOS() string {
	if _, err := os.Stat("/proc/version"); err == nil {
		return "linux"
	}
	return "windows"
}

func detectArch() string {
	return runtime.GOARCH
}
