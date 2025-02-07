package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "strconv"

    "github.com/gorilla/mux"
    "github.com/lib/pq" // Import pq driver
    "github.com/rs/cors"
    httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
    "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
    sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
)

const (
    host     = "localhost"
    port     = 5432
    user     = "go_user"
    password = "go_user"
    dbname   = "go_crud"
)

var db *sql.DB

type Item struct {
    ID          int     `json:"id"`
    Name        string  `json:"name"`
    Description string  `json:"description"`
    Price       float64 `json:"price"`
}

func main() {
    // Start Datadog tracer
    tracer.Start(
        tracer.WithAgentAddr("localhost:8126"),
        tracer.WithService("test-go"),
        tracer.WithEnv("prod"),
        tracer.WithServiceVersion("abc123"),
    )
    defer tracer.Stop()

    // Register the driver with Datadog tracing
    sqltrace.Register("postgres", &pq.Driver{}, sqltrace.WithDBMPropagation(tracer.DBMPropagationModeFull))

    psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
        host, port, user, password, dbname)

    var err error
    db, err = sqltrace.Open("postgres", psqlInfo)
    if err != nil {
        log.Fatalf("Error opening database: %v\n", err)
    }
    defer db.Close()

    err = db.Ping()
    if err != nil {
        log.Fatalf("Error connecting to the database: %v\n", err)
    }

    // Create a traced mux router
    muxRouter := mux.NewRouter()
    tracedMux := httptrace.NewServeMux()

    // Define routes
    muxRouter.HandleFunc("/items", createItem).Methods("POST")
    muxRouter.HandleFunc("/items", getItems).Methods("GET")
    muxRouter.HandleFunc("/items/{id}", getItem).Methods("GET")
    muxRouter.HandleFunc("/items/{id}", updateItem).Methods("PUT")
    muxRouter.HandleFunc("/items/{id}", deleteItem).Methods("DELETE")

    tracedMux.Handle("/", muxRouter)

    // CORS setup
    c := cors.New(cors.Options{
        AllowedOrigins:   []string{"http://localhost:3000"}, // Update with your frontend URL
        AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE"},
        AllowedHeaders:   []string{"Content-Type"},
        AllowCredentials: true,
    })
    handler := c.Handler(tracedMux)

    log.Println("Server started on :8000")
    log.Fatal(http.ListenAndServe(":8000", handler))
}

func traceHTTPHandler(fn http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        span, ctx := tracer.StartSpanFromContext(ctx, r.Method+" "+r.URL.Path)
        defer span.Finish()
        fn(w, r.WithContext(ctx))
    }
}

func createItem(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    span, _ := tracer.StartSpanFromContext(ctx, "createItem", tracer.ResourceName("INSERT INTO items"))
    defer span.Finish()

    var item Item
    err := json.NewDecoder(r.Body).Decode(&item)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sqlStatement := `INSERT INTO items (name, description, price) VALUES ($1, $2, $3) RETURNING id`
    err = db.QueryRowContext(ctx, sqlStatement, item.Name, item.Description, item.Price).Scan(&item.ID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(item)
}

func getItems(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    span, _ := tracer.StartSpanFromContext(ctx, "getItems", tracer.ResourceName("SELECT id, name, description, price FROM items"))
    defer span.Finish()

    rows, err := db.QueryContext(ctx, "SELECT id, name, description, price FROM items")
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    items := []Item{}
    for rows.Next() {
        var item Item
        err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Price)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        items = append(items, item)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(items)
}

func getItem(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    span, _ := tracer.StartSpanFromContext(ctx, "getItem", tracer.ResourceName("SELECT id, name, description, price FROM items WHERE id = $1"))
    defer span.Finish()

    params := mux.Vars(r)
    id, err := strconv.Atoi(params["id"])
    if err != nil {
        http.Error(w, "Invalid item ID", http.StatusBadRequest)
        return
    }

    var item Item
    sqlStatement := `SELECT id, name, description, price FROM items WHERE id = $1`
    err = db.QueryRowContext(ctx, sqlStatement, id).Scan(&item.ID, &item.Name, &item.Description, &item.Price)
    if err != nil {
        if err == sql.ErrNoRows {
            http.Error(w, "Item not found", http.StatusNotFound)
            return
        }
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(item)
}

func updateItem(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    span, _ := tracer.StartSpanFromContext(ctx, "updateItem", tracer.ResourceName("UPDATE items"))
    defer span.Finish()

    params := mux.Vars(r)
    id, err := strconv.Atoi(params["id"])
    if err != nil {
        http.Error(w, "Invalid item ID", http.StatusBadRequest)
        return
    }

    var item Item
    err = json.NewDecoder(r.Body).Decode(&item)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sqlStatement := `UPDATE items SET name = $1, description = $2, price = $3 WHERE id = $4`
    _, err = db.ExecContext(ctx, sqlStatement, item.Name, item.Description, item.Price, id)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusNoContent)
}

func deleteItem(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    span, _ := tracer.StartSpanFromContext(ctx, "deleteItem", tracer.ResourceName("DELETE FROM items WHERE id = $1"))
    defer span.Finish()

    params := mux.Vars(r)
    id, err := strconv.Atoi(params["id"])
    if err != nil {
        http.Error(w, "Invalid item ID", http.StatusBadRequest)
        return
    }

    sqlStatement := `DELETE FROM items WHERE id = $1`
    _, err = db.ExecContext(ctx, sqlStatement, id)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusNoContent)
}
