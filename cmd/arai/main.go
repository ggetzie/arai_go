// Command arai runs askredditbut.ai.
//
//	arai serve   [-env file]   run the web server
//	arai select  [-env file]   choose today's questions (run daily from cron)
//	arai migrate [-env file]   apply the Go port's schema additions
//	arai post reddit|x [-env file] [-dry-run]
//	                           post the top-voted question not yet posted there
//	arai hashpw                print a bcrypt hash for ARAI_ADMIN_PASSWORD_HASH
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/ggetzie/arai_go/internal/config"
	"github.com/ggetzie/arai_go/internal/post"
	"github.com/ggetzie/arai_go/internal/store"
	"github.com/ggetzie/arai_go/internal/web"
)

const usage = `usage: arai <command> [-env file]

commands:
  serve     run the web server
  select    choose today's questions (run daily from cron)
  migrate   apply the Go port's schema additions
  post      post the next upvoted question: arai post reddit|x [-env file] [-dry-run]
  hashpw    print a bcrypt hash for ARAI_ADMIN_PASSWORD_HASH
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "serve":
		err = withStore(args, log, serve)
	case "select":
		err = withStore(args, log, selectQuestions)
	case "migrate":
		err = withStore(args, log, func(ctx context.Context, _ *config.Config, st *store.Store, log *slog.Logger) error {
			if err := st.Migrate(ctx); err != nil {
				return err
			}
			log.Info("migrations applied")
			return nil
		})
	case "post":
		if len(args) == 0 || (args[0] != string(store.Reddit) && args[0] != string(store.X)) {
			fmt.Fprintf(os.Stderr, "usage: arai post reddit|x [-env file] [-dry-run]\n")
			os.Exit(2)
		}
		platform := store.Platform(args[0])
		var dryRun bool
		err = withStore(args[1:], log, func(ctx context.Context, _ *config.Config, st *store.Store, log *slog.Logger) error {
			return postNext(ctx, st, platform, dryRun, log)
		}, func(fs *flag.FlagSet) {
			fs.BoolVar(&dryRun, "dry-run", false, "show the question that would be posted, without posting")
		})
	case "hashpw":
		err = hashPassword()
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

type storeCmd func(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) error

// withStore parses the common -env flag (plus any flags added by extraFlags),
// loads config and opens the database.
func withStore(args []string, log *slog.Logger, run storeCmd, extraFlags ...func(*flag.FlagSet)) error {
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	envFile := fs.String("env", "", "load environment variables from this KEY=VALUE file")
	for _, f := range extraFlags {
		f(fs)
	}
	fs.Parse(args)
	if *envFile != "" {
		if err := config.LoadEnvFile(*envFile); err != nil {
			return fmt.Errorf("load env file: %w", err)
		}
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Open(connectCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	return run(ctx, cfg, st, log)
}

func serve(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) error {
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	srv, err := web.New(cfg, st, log)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "debug", cfg.Debug)
		errc <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func selectQuestions(ctx context.Context, _ *config.Config, st *store.Store, log *slog.Logger) error {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	selected, done, err := st.SelectDaily(ctx, today)
	if err != nil {
		return err
	}
	if done {
		log.Info("questions already selected for today", "date", today.Format(time.DateOnly))
	} else {
		log.Info("selected questions", "date", today.Format(time.DateOnly), "count", len(selected))
		for _, q := range selected {
			fmt.Println(q.Text)
		}
	}
	pruned, err := st.PruneVoteStates(ctx, now.AddDate(0, 0, -30))
	if err != nil {
		return err
	}
	log.Info("pruned old vote states", "rows", pruned)
	return nil
}

func hashPassword() error {
	var pw []byte
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		p1, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Again: ")
		p2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if string(p1) != string(p2) {
			return errors.New("passwords do not match")
		}
		pw = p1
	} else {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			return errors.New("no password given on stdin (run from a terminal, or pipe the password in)")
		} else if err != nil && line == "" {
			return err
		}
		pw = []byte(strings.TrimRight(line, "\r\n"))
	}
	if len(pw) < 12 {
		return errors.New("use a password of at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword(pw, 12)
	if err != nil {
		return err
	}
	fmt.Println(string(hash))
	return nil
}

// postNext posts the top-voted question that hasn't been posted to platform
// yet and marks it posted. Having nothing to post is not an error.
func postNext(ctx context.Context, st *store.Store, platform store.Platform, dryRun bool, log *slog.Logger) error {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	if dryRun {
		q, err := st.NextToPost(ctx, platform, today)
		if errors.Is(err, store.ErrNothingToPost) {
			log.Info("nothing to post", "platform", platform)
			return nil
		} else if err != nil {
			return err
		}
		log.Info("would post", "platform", platform, "question_id", q.ID, "votes", q.Votes, "displayed", q.Displayed.Format(time.DateOnly))
		fmt.Println(q.Text)
		return nil
	}

	var publish func(context.Context, string) (string, error)
	switch platform {
	case store.Reddit:
		r, err := config.Reddit()
		if err != nil {
			return err
		}
		publish = r.Submit
	case store.X:
		x, err := config.X()
		if err != nil {
			return err
		}
		publish = func(ctx context.Context, text string) (string, error) {
			id, err := x.Post(ctx, text)
			if errors.Is(err, post.ErrDuplicate) {
				// Already on the account, so count it as posted rather than
				// retrying it forever.
				log.Warn("x reports a duplicate; marking the question as tweeted", "err", err)
				return "", nil
			}
			return id, err
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	q, externalID, err := st.PostNext(callCtx, platform, today, func(q store.Question) (string, error) {
		return publish(callCtx, q.Text)
	})
	switch {
	case errors.Is(err, store.ErrNothingToPost):
		log.Info("nothing to post", "platform", platform)
		return nil
	case err != nil:
		if q.ID != 0 {
			return fmt.Errorf("question %d: %w", q.ID, err)
		}
		return err
	}
	log.Info("posted", "platform", platform, "question_id", q.ID, "votes", q.Votes, "post_id", externalID, "text", q.Text)
	return nil
}
