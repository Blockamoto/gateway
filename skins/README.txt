GATEWAY CLIENT SKINS — v0.4.8

The UI is a client of the local BOD API rather than a privileged part of the
resolver. The bundled UI is "default". A skin is simply a directory whose root
contains index.html plus any CSS/JS/assets it wants.

Run a custom skin:

  blocks-on-demand-windows-x64.exe -skin "C:\\path\\to\\my-skin"

or on Linux:

  ./blocks-on-demand-linux-x64 -skin ./skins/minimal

Custom skins should target /api/v1/*. See SKIN-API-v1.txt.

The skin runs same-origin on BOD's localhost HTTP server. It never needs direct
access to BOD's block cache, headers, Bitcoin Core credentials, or P2P sockets.
That separation is intentional: skins render; the BOD core resolves/verifies.
