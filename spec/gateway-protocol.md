# Gateway On Demand protocol v1 — experimental (BOD app 0.4.2; wire unchanged from 0.3.9)

Gateway On Demand is a generic service-discovery protocol carried on an ordinary Bitcoin
P2P connection. It exists so Bitcoin on Demand, Satline, Bitmap networking and
future protocols can share one discovery substrate without making BOD itself a
registry for every future application.

## Discovery

1. Bootstrap/discover ordinary Bitcoin peers using normal Bitcoin mechanisms.
2. Experimental service bit 25 (`1ULL << 25`) means **possible Gateway On Demand peer**.
3. Connect and perform the normal Bitcoin `version` exchange.
4. For peers with protocol version >= 70017, BIP 434 negotiates the Gateway
   feature family.
5. Complete `verack`.
6. Exchange a bounded `gwmsg` hello containing directly supported protocol IDs
   and versions.
7. If both sides list a compatible service protocol, that service may begin.

The bit is only a hint. BIP 434 confirms Gateway On Demand. A Gateway On Demand protocol listing is
still only a capability claim; successful service negotiation/behavior proves
that the implementation actually speaks it.

## Feature identity

Experimental BIP 434 feature ID:

`8f01b6e7d640f7543b44c1d8a5f5d626559d512ff6d37d60d069761460250a7c`

Gateway On Demand wire version: `1`.

## `gwmsg`

Maximum message payload: 16 KiB.

Hello:

```json
{
  "wire": 1,
  "kind": "hello",
  "id": "random request id",
  "protocols": [
    {
      "id": "53e14b718ca5ba11bff160a78e6528b890f7be5d20060e411ddaec3c1f23bdfe",
      "versions": [1],
      "name": "Bitcoin on Demand"
    }
  ]
}
```

The peer replies with `kind: "hello_ack"`, the same `id`, and its own directly
supported protocol list.

Human-readable names are informational only. Machine identity is the stable
protocol-family ID; versions are advertised separately.

Gateway On Demand v1 does not gossip "what somebody else supports". It says only what the
connected peer itself speaks.

## Security boundary

Gateway On Demand does not authenticate a protocol author and does not turn capability
claims into trusted facts. A peer can copy a public protocol ID. Service
protocols therefore remain responsible for their own validation semantics.
Signed protocol manifests may be added in a later version if there is a concrete
need; they are not part of v0.4.2.
