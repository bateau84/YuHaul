// Command migrate performs the one-time historical corpus import for YuHaul.
//
// Usage:
//
//	migrate --source-db=/path/to/old.db --dest-db=/path/to/yuhaul.db [--batch-id=uuid]
//
// The source database must contain exactly one table with both video_id and
// title columns. If the schema cannot be discovered, the command reports the
// blocked precondition and exits with a non-zero status.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/bateau84/YuHaul/internal/migration"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

func main() {
	var (
		sourceDB = flag.String("source-db", "", "path to the source SQLite database")
		destDB   = flag.String("dest-db", "", "path to the destination YuHaul SQLite database")
		batchID  = flag.String("batch-id", "", "import batch identifier (generated if empty)")
	)
	flag.Parse()

	if *sourceDB == "" || *destDB == "" {
		flag.Usage()
		os.Exit(2)
	}

	if *batchID == "" {
		*batchID = uuid.NewString()
	}

	ctx := context.Background()
	logger := slog.Default()

	if err := run(ctx, logger, *sourceDB, *destDB, *batchID); err != nil {
		if errors.Is(err, migration.ErrSchemaNotDiscoverable) {
			logger.Error("blocked precondition: source database schema is not discoverable",
				slog.String("source_db", *sourceDB),
				slog.String("error", err.Error()),
			)
			fmt.Fprintf(os.Stderr, "blocked precondition: %v\n", err)
			os.Exit(3)
		}
		logger.Error("migration failed", slog.String("error", err.Error()))
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, sourcePath, destPath, batchID string) error {
	source, err := openSourceDB(sourcePath)
	if err != nil {
		return fmt.Errorf("open source database: %w", err)
	}
	defer source.Close()

	dest, err := store.Open(ctx, destPath)
	if err != nil {
		return fmt.Errorf("open destination database: %w", err)
	}
	defer dest.Close()

	logger.Info("discovering source schema",
		slog.String("source_db", sourcePath),
		slog.String("dest_db", destPath),
	)
	schema, err := migration.DiscoverSchema(ctx, source)
	if err != nil {
		return err
	}
	logger.Info("source schema discovered",
		slog.String("table", schema.TableName),
		slog.String("video_id_column", schema.VideoIDColumn),
		slog.String("title_column", schema.TitleColumn),
		slog.String("channel_id_column", schema.ChannelIDColumn),
	)

	imp := migration.NewImporter(source, dest, batchID)
	result, err := imp.Run(ctx, schema)
	if err != nil {
		return err
	}

	logger.Info("migration complete",
		slog.String("batch_id", batchID),
		slog.Int("read", result.Read),
		slog.Int("inserted", result.Inserted),
		slog.Int("skipped", result.Skipped),
		slog.Int("errors", result.Errors),
	)
	fmt.Printf("Migration complete.\n  Batch: %s\n  Read: %d\n  Inserted: %d\n  Skipped: %d\n  Errors: %d\n",
		batchID, result.Read, result.Inserted, result.Skipped, result.Errors)

	return nil
}

func openSourceDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
