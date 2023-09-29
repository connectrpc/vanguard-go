# OpenLibrary Proxy

Open Library proxy creates a vanguard service to serve [books.proto](./proto/openlibrary/v1/books.proto].

## Demo

Run the proxy server with:
```
go run .
```

Now we can use `buf curl` to call OpenLibrary's REST API via our newly created proxy.

### Get a book

```shell
curl --location 'https://openlibrary.org/api/books?bibkeys=ISBN:0201558025,LCCN:93005405&format=json'
```

```shell
buf curl --schema=./proto http://localhost:8080/openlibrary.v1.BooksService/GetBooks --data '{"bibkeys": "ISBN:0201558025,LCCN:93005405"}'
```

### Search for books

Search currently can't translate due to duplicate field errors:
```
{
   "code": "internal",
   "message": "proto: (line 17562:5): duplicate field \"num_found\""
}
```

```shell
curl --location 'http://openlibrary.org/search.json?q=the%2Blord%2Bof%2Bthe%2Brings
```

```shell
buf curl --schema=./proto http://localhost:8080/openlibrary.v1.BooksService/SearchBooks \
    --data '{"q": "the lord of the rings"}'
```

