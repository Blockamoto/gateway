GATEWAY SKINS

The UI is a client of Gateway's local API. The bundled workspace is the normal
interface. A custom skin is a directory containing index.html and its assets.
It does not unlock additional indexes or bypass API permissions.

Run a custom skin:

  GatewayClient.exe -skin .\skins\minimal

or on Linux:

  ./gateway-client -skin ./skins/minimal

The minimal skin demonstrates /api/v1/search. Prefer known-block coordinates
such as 1.170.bitcoin when testing. Additional index-dependent queries remain
locked. See extension.go and web.go for the implemented local API and
DEVELOPMENT.md for feature and storage boundaries.

Custom skin JavaScript runs on Gateway's local origin: install only a skin you
trust. It can make the local requests allowed to that UI session. Skins do not
need direct filesystem access, Core credentials or peer sockets; Gateway owns
resolution and validation. Keep the local server on loopback for ordinary use.
