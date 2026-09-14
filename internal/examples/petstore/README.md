# Pet Store

Example app that calls the [PetStore API][petstore] with an RPC client. The
client's transport comes from Vanguard, which translates each RPC into the REST
request described by the method's `google.api.http` annotation.

[petstore]: https://petstore.swagger.io/
