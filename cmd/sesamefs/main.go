package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Sesame-Disk/sesamefs/internal/api"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/joho/godotenv"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Parse command
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "serve")
	}

	command := os.Args[1]

	switch command {
	case "serve":
		runServer()
	case "health":
		runHealthCheck()
	case "migrate":
		runMigrations()
	case "version":
		printVersion()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Available commands: serve, health, migrate, version")
		os.Exit(1)
	}
}

func runServer() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize database connection
	database, err := db.New(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Create and start the API server
	server := api.NewServer(cfg, database)

	log.Printf("SesameFS %s starting on port %s", Version, cfg.Server.Port)
	if err := server.Run(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func runHealthCheck() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("UNHEALTHY: Failed to load config")
		os.Exit(1)
	}

	// TODO: Check database connection
	// TODO: Check storage connection

	fmt.Printf("HEALTHY: SesameFS on port %s\n", cfg.Server.Port)
	os.Exit(0)
}

func runMigrations() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	database, err := db.New(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	log.Println("Running database migrations...")
	if err := database.Migrate(); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Migrations completed successfully")
}

func printVersion() {
	fmt.Printf("SesameFS %s\n", Version)
	fmt.Printf("  Build Time: %s\n", BuildTime)
	fmt.Printf("  Git Commit: %s\n", GitCommit)
}
