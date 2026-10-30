# Brotli decoder source

`decode.go` and `reader.go` come from the pure-Go decoder in
google/brotli v1.2.1, commit `db24a98aed38a16de78cc05ca15cdd8dc2a0d92d`.
The upstream MIT license is in `LICENSE` and the bundled notices. No network
access or C compiler is needed to build this package.

Gateway changes one end-of-stream check in `decode.go` to accept trailing bytes
after the first complete stream, matching the pinned Ord reader. Truncation and
invalid stream checks remain enabled. `reader.go` and `LICENSE` are unmodified.

SHA-256 of the original upstream bytes (before Gateway's one-line decoder change):

- `decode.go`: `ef0b000387b328ba25bc2b8f40e1e0cc922b2585b502bbfee56050952b8bab38`
- `reader.go`: `12c5726315be628534bb09512a2a0d2ef038b3cc0bdd3c09b5faffd0a85c7de0`
- `LICENSE`: `3d180008e36922a4e8daec11c34c7af264fed5962d07924aea928c38e8663c94`
