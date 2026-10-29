# Gateway 0.7.9 testing and acceptance

The development evidence in `docs/VERIFICATION-0.7.9.md` in the source repository describes focused tests and their source and fixture limits. It does not by itself establish acceptance of the final release source or packaged binaries. The release candidate acceptance record is RELEASE-ACCEPTANCE-0.7.9.json in the source and RELEASE-ACCEPTANCE.json in the package.

Before publication, freeze the final committed source and record checks against that exact revision. Run the required source and regression checks, including the revised Parcel setup, scheduler and validated proof routes, and report failures or skips. Build in a neutral workspace using a completed validated header baseline, then independently verify the package manifest, all artifact hashes and sizes, ZIP contents, installer payload and build path privacy. Exercise the exact built binaries with separate startup/restart, browser, indexing and installer checks; retain the evidence and identify the binaries by hash.

After publication, verify the GitHub tag and downloaded release artifacts against the accepted source and hashes. Separately prepare and verify the signed update candidate, promote it only with authenticated frozen identities, then verify hosted application and header manifests and actual downloads. Test upgrade from a prior client separately and record its result. These checks are pending until independently recorded.

Synthetic and bounded real-block fixtures cannot establish full-mainnet performance or complete Parcel validation. Missing or unsupported proof remains pending, and header anchoring is not full Bitcoin consensus or script validation. Windows binaries do not establish Authenticode signing or SmartScreen acceptance without separate evidence.
