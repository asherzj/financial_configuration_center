# FinConfig examples

- `sdk/example_test.go` is executable Go documentation for client startup, immutable local query, explicit decode, subscription and two independent Region clients. The production composition point supplies the Kitex/mTLS/JWT transport.
- `http/admin-api.http` is a REST Client collection for OIDC session inspection, QueryPage `ALL` and `ONLY_DATA`, an intentional validation error, ReleaseOrder creation/action and CSRF-protected logout.

Run the SDK example with:

```sh
go test -run Example ./examples/sdk
```

The HTTP collection deliberately contains no real token, cookie, credential, record revision or environment URL. Replace variables from your local deployment and always refresh `ALL` data before forming a release diff.
