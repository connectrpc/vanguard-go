# Stripe Proxy

Stripe proxy creates a vanguard proxy for stripe's [Payments Intents](https://stripe.com/docs/api/payment_intents).

Part of the API is replicated in the proto file [payment_intents.proto](./proto/stripe/v1/payments).

## Demo

Run the proxy server with:

```
go run .
```

Build the schema with: 

```
buf build proto -o stripe.binpb
```

Now use `buf curl` to call Stripe's REST API via our newly created proxy:

### Create a Payment Intent
```shell
buf curl --schema=stripe.binpb http://localhost:8080/stripe.v1.PaymentIntentsService/CreatePaymentIntent --data '{"amount": 2000, "currency": "usd"}' -u sk_test_4eC39HqLyjWDarjtT1zdp7dc:
```

### Get a Payment Intent

```shell
buf curl --schema=stripe.binpb
    http://localhost:8080/stripe.v1.PaymentIntentsService/GetPaymentIntent 
    --data '{"id": "pi_1Gt0582eZvKYlo2CGSidzWqK"}'
    -u sk_test_4eC39HqLyjWDarjtT1zdp7dc:
```

Which is equivelent to the retrieve curl command in the [docs](https://stripe.com/docs/api/payment_intents/retrieve).

