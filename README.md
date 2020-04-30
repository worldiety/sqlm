# sqlm: A sql migration helper [draft] [wip]
*sqlm* takes multiple migration roots from within a single project (think of domain-driven design) for different
sql dialects and embedds them into your code, ready to be executed automatically at your application startup.

It can be used as a companion to [sqlc](https://github.com/kyleconroy/sqlc).

## why another migration tool

There are many alternatives like 
- [goose](https://github.com/pressly/goose)
- [sql-migrate](https://github.com/rubenv/sql-migrate)
- [tern](https://github.com/jackc/tern)
- [golang-migrate](https://github.com/golang-migrate/migrate)

However the libraries do not support the following requirements:
* multiple dialects in one project, sharing a common migration contract
* multiple independent migration sources in a single project
  * helps to keep the migration boundary as small as possible (ddd)
  * less need to distinguish between dev and release migration scripts
* resource embedding and startup migration as first class citizen
* go generate support for proper tool dependency management, so no command line
tooling required

Furthermore, it is not expected that "down"-migrations are so useful in practice, so
just relying on full transaction support should be sufficient.

## configuration
You need to create *sqlm.json* but may place it at arbitrary locations within your go module.

Example module structure
```
.
├── LICENSE
├── Makefile
├── go.mod
├── go.sum
└── service
    └── user
        └── repository
            ├── mysql
            │   ├── db.go
            │   ├── models.go
            │   ├── querier.go
            │   ├── query
            │   │   └── user.sql
            │   ├── schema
            │   │   ├── 0001_user.sql
            │   │   └── 0002_alteruser.sql
            │   └── user.sql.go
            ├── postgresql
            │   ├── db.go
            │   ├── models.go
            │   ├── querier.go
            │   ├── query
            │   │   └── user.sql
            │   ├── schema
            │   │   ├── 0001_user.sql
            │   │   └── 0002_alteruser.sql
            │   └── user.sql.go
            ├── repo.go
            ├── sqlc.json
            └── sqlm.json
```

All *.sql scripts are sorted per schema folder and evaluated in alphabetical order.
The namespace of each migration source is used from the *sqlm.json* and should be generally unique
per database.

Example *sqlm.json*

```json
{
  "version": "1",
  "packages": [
    {
      "group": "users",
      "path": "mysql",
      "pkgname": "mysql",
      "schema": "mysql/schema"
    },
    {
      "group": "users",
      "path": "postgresql",
      "pkgname": "postgresql",
      "schema": "postgresql/schema"
    }
  ]
}
```

## usage

Install
```bash
go get github.com/worldiety/sqlm
```

Create gen file
```go
// e.g. myproject/cmd/gen/gen.go
// go:generate go run gen.go
package main

import "github.com/worldiety/sqlm"

func main() {
    sqlm.Must(sqlm.GenerateAll("../..")) 
}
```

(re) generate
```bash
go generate ./...
```

invoke the migration in your app
```go
package main

import (
    "github.com/worldiety/myapp/service/user/repository/postgresql"
    "database/sql"
    "github.com/worldiety/sqlm"
    _ "github.com/go-sql-driver/mysql"
)


func main(){
    db, err := sql.Open("postgres", "...")
    if err != nil {
        panic(err)
    }
    sqlm.MustMigrate(db, postgresql.Migrations...)
}


```
