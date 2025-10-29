package migr

import (
    "context"
    "sort"
    "strings"
    _ "embed"
    "embed"

    "github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var fs embed.FS

// Apply executes all embedded SQL migrations in lexical order using the
// simple query protocol (supports multiple statements per file).
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
    conn, err := pool.Acquire(ctx)
    if err != nil { return err }
    defer conn.Release()

    entries, err := fs.ReadDir("migrations")
    if err != nil { return err }
    names := make([]string, 0, len(entries))
    for _, e := range entries {
        name := e.Name()
        if strings.HasSuffix(strings.ToLower(name), ".sql") {
            names = append(names, name)
        }
    }
    sort.Strings(names)
    for _, name := range names {
        b, err := fs.ReadFile("migrations/" + name)
        if err != nil { return err }
        res := conn.Conn().PgConn().Exec(ctx, string(b))
        if _, err := res.ReadAll(); err != nil { return err }
    }
    return nil
}

func splitSQL(s string) []string {
    var res []string
    buf := make([]rune, 0, len(s))
    inDollar := false
    inSingle := false
    inDouble := false
    runes := []rune(s)
    for i := 0; i < len(runes); i++ {
        r := runes[i]
        // detect $$ boundaries when not in quotes
        if !inSingle && !inDouble && r == '$' {
            // lookahead for $$
            if i+1 < len(runes) && runes[i+1] == '$' {
                inDollar = !inDollar
                buf = append(buf, r)
                buf = append(buf, runes[i+1])
                i++
                continue
            }
        }
        if !inDollar {
            if !inDouble && r == '\'' {
                // toggle single quote; handle escaped ''
                if i+1 < len(runes) && runes[i+1] == '\'' {
                    // escaped quote
                    buf = append(buf, r)
                    buf = append(buf, runes[i+1])
                    i++
                    continue
                }
                inSingle = !inSingle
            } else if !inSingle && r == '"' {
                inDouble = !inDouble
            }
        }
        if r == ';' && !inDollar && !inSingle && !inDouble {
            stmt := stringTrim(string(buf))
            if stmt != "" {
                res = append(res, stmt)
            }
            buf = buf[:0]
            continue
        }
        buf = append(buf, r)
    }
    tail := stringTrim(string(buf))
    if tail != "" { res = append(res, tail) }
    return res
}

func stringTrim(s string) string { return s }
