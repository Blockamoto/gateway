# Saved index exports in 0.7.8

In **Index settings**, Inscriptions and Bitmap Districts Standard V1 offer
**Export saved index**. Choose **Native blocators** or **Conventional inscription
IDs**, prepare the export, inspect its committed snapshot and saved coverage,
then download the file. Parcel execution and its normal interface entries are
disabled pending validation; saved Parcel records retain passive backend export
compatibility.

Exporting changes the file representation. It does not change an index's native
references, rules, profile, checkpoints, retention, Arm, Live or shared range.
It does not start, resume or pause indexing, download source history, repair
lookup stores, or install browser routing.

## Coverage and lookup requirements

A complete export contains all records in the stated saved scope. A partial
District conventional-ID export contains only verifiably translated winners.
Neither asserts that gaps or earlier/later Bitcoin history were indexed. Inscriptions
can include verified committed sparse browsing coverage as well as their saved
linear prefix; the file identifies the captured sources and coverage. Pending
work and staged, uncommitted indexing progress are excluded.

Conventional IDs use the reveal transaction ID followed by `i` and the
transaction's inscription occurrence number. They do not use a Parcel's target
transaction, witness transaction ID, input number or envelope-local position.
Existing authenticated inscription/transaction lookup information and retained
Raw bodies, verified local archive/cache blocks, and read-only mounted blocks
can supply the conversion. Export never fetches missing
blocks or constructs locators implicitly.

For a District conventional-ID export, unavailable translations do not prevent
an export: verified winner mappings are included and unresolved winners are
omitted. Review and the file report that the result is partial and give the
number of omitted District winners. The missing-reference count can be larger
than the number of omitted winners because the same inscription can occur in
more than one export record. It is not a complete District dataset. A conflict,
corruption or invalid selected-chain evidence still fails rather than becoming
an omission. Other export types still require complete identity conversion.

The review reports bounded missing examples and the height span containing
missing lookups. The span can include gaps and parent reveals outside the index's
own coverage. **Arm TX ID Locators** is an explicit staging action; it preserves
the current range and Live choices. Review the next required block and run range
before pressing Play. An existing sequential TX index resumes from its saved
checkpoint: it cannot jump a gap or prepend earlier history. Native export
remains a separate choice and does not claim that unknown conventional IDs are
known.

## File format

Files use UTF-8 JSON Lines (`gateway-index-export-jsonl-v1`): one JSON object per
line. A snapshot header identifies the index, representation, saved coverage
and original source checkpoint/profile commitments. Large coverage and sparse
source membership are written in complete, bounded chunks after a compact
header summary. Data records follow, then a completion footer with application
record counts and a checksum for all preceding file bytes. Coverage and
source-membership manifest lines are excluded from the application row count.
Source commitments attest the original stored evidence; they are not hashes
of the converted record bytes. The coverage describes the saved source scope,
not a claim that every District winner was translated. Partial District files
report omitted winners in the completion footer and unresolved source-block
references are explicitly marked incomplete.

The two representations use the same record layout. Only inscription identity
references are converted. Inscriptions export occurrence mappings and compact
retained Full metadata; content bodies and witness payloads are excluded. Block hashes, reveal anchors, target District and
transaction numbers, occurrence positions, output coordinates, Sat snapshots,
content hashes and rule outcomes retain their meaning.

Bitmap data rows describe canonical selected winners, once per saved target.
They are not a list of every competing or rejected claim. District rows pair
`bitmap` with `id`; for example, the same target can be represented as
`{"bitmap":0,"id":"1i2.792435"}` or
`{"bitmap":0,"id":"<64-character-reveal-transaction-id>i2"}`. The actual rows
also retain their source and evidence fields. Parcel targets retain their
separate District and transaction components. A Parcel target transaction is
not its reveal transaction.

The supplied [BoppletonLiveOCI comparison application](https://ordinals.com/content/0c1d5fbd2cf9a2bd33d53d444e4873f9b2efb54f823226f8a76ba0e4baa5cb11i0) was checked for parsing
District JSONL rows with numeric `bitmap` and conventional inscription `id`
fields, including a leading metadata line. Parser acceptance does not certify
the saved winners, their coverage, or the comparison result. Parcel and
Inscriptions exports have their own record meanings; they are not District
comparison lists.

## Snapshot and download protocol

Both endpoints accept local, same-origin JSON POST requests:

- `/api/v1/index/export/review`: `{index, representation}`.
- `/api/v1/index/export/download`: `{index, representation, snapshot}`, using
  the exact snapshot returned by a successful review.

Supported indexes are `inscriptions`, `bitmap-districts-standard-v1` and
`bitmap-parcels-standard-v1`. Representations are `native` and `conventional`.
Review reports readiness, coverage, source anchors, counts and requirements.
A successful review pins its exact output in this Gateway process for up to
15 minutes; the returned `expires_at` is an upper bound. Restarting Gateway or
evicting older reviews to stay within the bounded review capacity can expire it
sooner. A partial District conventional-ID review enables a clearly labelled
partial download; other incomplete reviews do not enable download. The process
keeps at most four reviews under an estimated 256 MiB aggregate retained-pin
budget. One generation also has a separate estimated 256 MiB transient identity
consistency budget, in addition to normal index and session buffers. A review
that exceeds a budget fails explicitly; these estimates are not RSS limits.

Download revalidates the reviewed committed sources and selected-chain anchors
and prepares the entire artifact in a private temporary spool before sending
attachment headers. It compares the completed bytes with the reviewed artifact.
Missing, deleted, reorganized, changed or conflicting evidence produces an
error response rather than an unverified file. Expired, evicted or lost
reviews require a fresh preparation. A successful response confirms that the artifact transfer is complete separately
from whether the dataset is complete, and includes the snapshot, byte length
and export checksum. The UI checks those headers and receives the complete
artifact before offering it as a download.
Temporary spools are removed on success, failure and cancellation.

New append-only progress does not change or invalidate the reviewed snapshot;
it is not added to the downloaded file. Export does not control the ongoing run.

## Verification

Focused Go tests cover identity conversion, source/snapshot binding, missing
coverage, read-only behavior and HTTP attachment failure boundaries. Browser
checks cover both representations in all three settings, actual downloads,
explicit locator staging, unchanged range/Live choices, and stale requests.
Compiled-application checks use separate disposable profiles. They never use
the user's actively indexing preview profile.
