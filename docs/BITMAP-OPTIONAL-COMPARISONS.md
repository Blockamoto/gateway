# Optional Bitmap comparison rules

The 16 October 2026 development update adds two optional questions for District and Parcel profiles. Both default to **0 / Off**. The current Standard continues to evaluate the inscription's own body using its existing media-type and declared-charset policy.

The creator-reviewed reference is [bitmap-old, pinned revision](https://github.com/Blockamoto/bitmap-old/tree/ccf7271a61a2cead4781d4a2bb9c86da761f1cd6). Its [shared comparison specification](https://github.com/Blockamoto/bitmap-old/blob/ccf7271a61a2cead4781d4a2bb9c86da761f1cd6/COMPARISON-OPTIONS.md), [District questions](https://github.com/Blockamoto/bitmap-old/blob/ccf7271a61a2cead4781d4a2bb9c86da761f1cd6/DISTRICT-RULESET.md) and [Parcel questions](https://github.com/Blockamoto/bitmap-old/blob/ccf7271a61a2cead4781d4a2bb9c86da761f1cd6/PARCEL-RULESET.md) define the additions. Numeric bit positions and a canonical cross-implementation profile serialization remain unfrozen.

| Option | Off / 0 | On / 1 |
| --- | --- | --- |
| Reject delegate declarations (`delegate_exclusion`) | Declaration alone does not reject an otherwise eligible own-body claim. | Reject an exact delegate declaration in the envelope field section, even if the own body is valid or the target cannot be resolved. |
| UTF-8 bytes (`raw_utf8_body`) | Preserve the profile's existing decoder. | Validate the original own-body bytes as strict UTF-8 and match the complete claim directly, ignoring media-type and charset metadata. |

## Delegate declarations

The [official Ordinals specification](https://docs.ordinals.com/inscriptions/delegate.html) assigns the delegate field tag 11 decimal. The optional exclusion inspects the authenticated envelope's field section, rather than fetching delegated content. Empty, malformed and repeated delegate values count as declarations; the explicit comparison policy also rejects a dangling final delegate tag. Bytes in the body or a field value are not tag declarations. A missing decoded delegate ID does not establish absence of its raw field.

Standard own-body selection and delegate exclusion are different choices. The former specifies which bytes to inspect; the latter can reject the whole candidate because it declares a delegate. A delegate target does not need to exist to apply the exclusion. Unavailable authenticated envelope evidence remains unknown and cannot be treated as a pass.

## UTF-8 bytes and decoder dependencies

UTF-8 byte matching performs no charset guessing, invalid-byte replacement, trimming, normalization, BOM removal, decompression or delegate substitution. The existing whole District/Parcel claim grammar still applies, including ASCII digits and lowercase suffix. The option does not enable Unicode digits or permissive substring matching.

| `text` | `raw_utf8_body` | Meaning |
| :---: | :---: | --- |
| 1 | 0 | Current Standard media-type and declared-charset interpretation. |
| 0 | 1 | Explicit raw UTF-8 comparison. |
| 1 | 1 | Conflicting decoder choices; the profile cannot be admitted. |
| 0 | 0 | The profile's explicitly identified alternative; existing Gateway raw-ASCII profiles retain their exact meaning. |

Raw UTF-8 mode also conflicts with eligibility solely through Brotli decompression. Because valid canonical claims are ASCII, raw ASCII and raw UTF-8 need not choose different winners. Media-type/charset bypass can differ from Standard evaluation. A changed ruleset hash alone does not establish an outcome difference.

## Existing profiles and results

The additions use a separately versioned named profile. Existing profiles retain their original reference, answer maps, decoder policies and hashes. Registering the new questions must not silently rehash or relabel saved results. Rule classification and a track's editable display name remain separate. Details shows the complete answer string beside the track name, with its Gateway rule version and ordered key; this is not an assertion of canonical cross-implementation bit positions. Legacy tracks show a clearly labelled projection of their fixed policy without changing their stored identity.

This reader accepts preserved older records alongside new v3 profiles. Older development clients may not understand a registry after v3 profiles are explicitly added; preserving old hashes does not make new interpretations executable by an old client.

Built interpretations stay inspectable and locked. Trying a different interpretation requires a separate custom track and explicit evaluation. Older candidate/winner rows without delegate-declaration evidence cannot prove that the delegate field was absent: use authenticated reveal/occurrence evidence or explain the required replay. No hidden Satline, numbering or full-chain build is authorized by merely viewing or editing a profile.

These are optional creator-requested comparison semantics. No equivalence with a particular third-party indexer is established without pinned source and matching executable fixtures. Bitmap Chain framing is unchanged.
