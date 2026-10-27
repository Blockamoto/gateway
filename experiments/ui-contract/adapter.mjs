// Proposed v1 normalized adapter, not a deployed Gateway API.
// Preserve the native document during migration; never turn missing evidence
// into an empty complete range or a definite negative result.
const clone = value => value === undefined ? undefined : structuredClone(value);
const plain = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const finiteRevision = value => Number.isSafeInteger(value) && value >= 0;
const boolOrUnknown = value => typeof value === 'boolean' ? value : null;

function intervalList(value, label) {
  if (value === undefined || value === null) return null;
  if (!Array.isArray(value)) throw new TypeError(`${label} must be an array or unknown`);
  return value.map((r) => {
    if (!plain(r) || !Number.isSafeInteger(r.from) || !Number.isSafeInteger(r.to) || r.from < 0 || r.to < r.from)
      throw new TypeError(`${label} has an invalid closed block interval`);
    return {from:r.from, to:r.to};
  });
}

export function normalizeLegacyStatus(status) {
  if (!plain(status) || !plain(status.timeline) || !Array.isArray(status.timeline.tracks) || !plain(status.run))
    throw new TypeError('missing 0.7.8 status/timeline/run document');
  const ids = new Set();
  const tracks = status.timeline.tracks.map(track => {
    if (!plain(track) || typeof track.id !== 'string' || !track.id || ids.has(track.id))
      throw new TypeError('invalid or duplicate timeline track');
    ids.add(track.id);
    if (track.sources != null && !Array.isArray(track.sources)) throw new TypeError('invalid track sources');
    const sources = track.sources == null ? null : track.sources.map(source => {
      if (!plain(source) || typeof source.id !== 'string' || typeof source.kind !== 'string') throw new TypeError('invalid coverage source');
      return {id:source.id, kind:source.kind, ranges:intervalList(source.ranges,'source ranges'),
        enumerationComplete:boolOrUnknown(source.complete), verification:source.verification ?? null, note:source.note ?? null};
    });
    const r = track.retained_coverage;
    if (r !== undefined && r !== null && !plain(r)) throw new TypeError('invalid retained coverage');
    const retainedRaw = r == null ? null : {scope:r.scope ?? null, state:r.state ?? null,
      enumerationComplete:boolOrUnknown(r.complete), revision:r.revision ?? null, ranges:intervalList(r.ranges,'retained ranges'),
      totalBlocks:r.total_blocks ?? null, observedBlocks:r.observed_blocks ?? null};
    return {id:track.id, locked:boolOrUnknown(track.locked),
      localCoverage:{ranges:intervalList(track.coverage,'coverage'), enumerationComplete:boolOrUnknown(track.coverage_complete)},
      gaps:intervalList(track.gaps,'gaps'), recorded:intervalList(track.recorded_coverage,'recorded coverage'),
      providerReported:intervalList(track.reported_coverage,'reported coverage'), sources, retainedRaw,
      backfill:clone(track.backfill) ?? null, potentialFrom:track.potential_from ?? null, note:track.note ?? null};
  });
  if (status.observation != null && !plain(status.observation)) throw new TypeError('invalid observation');
  const observation = status.observation == null ? null : {
    mode:status.observation.mode ?? null, detailsObservedAt:status.observation.details_observed_at ?? null,
    detailsAgeMilliseconds:status.observation.details_age_milliseconds ?? null,
    detailsRefreshing:boolOrUnknown(status.observation.details_refreshing)
  };
  return {contract:'gateway.workspace/1', source:'gateway-0.7.8', observation,
    timeline:{observedAt:status.timeline.observed_at ?? null, tipKnown:boolOrUnknown(status.timeline.tip_known),
      tipHeight:status.timeline.tip_height ?? null, tipHash:status.timeline.tip_hash ?? null, tracks},
    run:clone(status.run),
    // Keep all original fields until the existing UI's independent cards,
    // explorer, per-index options and extension consumers are mapped.
    legacy:clone(status)};
}

// A run revision is distinct from the age/revision of coverage observations.
// Never replace a newer run with an older asynchronous run response.
export function keepNewestRun(previous, incoming) {
  const p=previous?.run, n=incoming?.run;
  if (plain(p) && plain(n) && finiteRevision(p.revision) && finiteRevision(n.revision) && p.revision>n.revision)
    return {...incoming,run:clone(p)};
  return clone(incoming);
}

function requestObject(value) { if (!plain(value)) throw new TypeError('request must be an object'); return clone(value); }
function nonempty(value,label) { if (typeof value !== 'string' || !value) throw new TypeError(`${label} required`); return value; }
const post=(route,body)=>({method:'POST',path:'/api/v1/index/'+route,body});
export const legacyCommands = {
  prepare(request) { return post('run/prepare',requestObject(request)); },
  plan(request) { return post('run/plan',requestObject(request)); },
  start(request, planId) {
    const body={request:requestObject(request)};
    if (planId !== undefined) body.plan_id=nonempty(planId,'plan ID');
    return post('run/start',body);
  },
  amend(runId, admissionRevision, consumers) {
    nonempty(runId,'run ID');
    if (!finiteRevision(admissionRevision) || admissionRevision < 1 || !Array.isArray(consumers)) throw new TypeError('invalid amendment');
    return post('run/amend',{run_id:runId, admission_revision:admissionRevision, consumers:clone(consumers)});
  },
  control(runId, action) {
    nonempty(runId,'run ID');
    if (!['pause','resume','cancel'].includes(action)) throw new TypeError('invalid run action');
    return post('run/control',{id:runId,action});
  },
  blockMap(query) { return {method:'GET',path:'/api/v1/index/block-map?block='+encodeURIComponent(nonempty(query,'block query'))}; }
};

export function legacyError(status, payload) {
  if (!Number.isInteger(status) || status < 400 || !plain(payload) || typeof payload.error !== 'string')
    throw new TypeError('invalid legacy error');
  // Legacy messages are not a stable machine-readable taxonomy. Keep plan and
  // the native payload available rather than guessing from English text.
  return {contract:'gateway.workspace/1', code:'legacy_http_error', httpStatus:status,
    message:payload.error, details:clone(payload)};
}
