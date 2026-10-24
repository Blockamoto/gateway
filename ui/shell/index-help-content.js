'use strict';
// Read-only wording. Rendering, focus, dismissal and controls belong to index-help.js.
// Source inputs are deliberately distinct from additional retained-index requirements.
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.GatewayIndexHelpContent = api;
})(typeof window === 'object' ? window : null, function () {
  const purposes = {
    'group:blocks': {
      title: 'Blocks',
      paragraphs: [
        'Headers identify Bitcoin blocks and their order. Raw keeps the complete blocks, including their transactions. Use this group to see which chain information and full block bodies are available locally.',
        'Saving a block and saving facts calculated from it are separate choices. Each track shows its own saved coverage; a saved Raw block does not mean the transaction, address or sat tracks have been built.'
      ]
    },
    'group:txio': {
      title: 'Transactions',
      paragraphs: [
        'These tracks help you find transactions, follow which outputs they spend, inspect amounts and payment conditions, and track unspent outputs or individual sats.',
        'Each track answers a different question. A known amount is not proof that an output is unspent, and a known spending link is not a complete wallet balance. Choose the data useful to you; the group heading does not make the tracks interchangeable.'
      ]
    },
    'group:addresses': {
      title: 'Addresses',
      paragraphs: [
        'Current Addresses shows unspent outputs for a supported address at a saved block. Historical Outputs shows outputs created for that address in the blocks you have scanned.',
        'Use Current Addresses to inspect a balance at its stated block, and Historical Outputs to investigate payments over a selected range. Historical received amounts are not a current balance.',
        'A supported address can also be read from an individual output’s payment conditions without building either address track.'
      ]
    },
    'group:bitmap': {
      title: 'Bitmap',
      paragraphs: [
        'Districts and Parcels keep Bitmap claim results and connect them to their Bitcoin blocks and inscription reveal locations. Use them to inspect the claim selected for a numbered block or parcel.',
        'The Rules view contains the exact claim rules. This help explains what the tracks store; it does not add claim rules or establish current inscription ownership.',
        'Bitmap Chains are disabled while their method is being developed. Existing saved Chain evidence remains available for inspection.'
      ]
    },
    headers: {
      title: 'Headers',
      paragraphs: [
        'Keeps the small header for each block in the selected Bitcoin chain. Headers identify blocks and connect them in order, so Gateway can check that a fetched block is the expected one.',
        'Use the header timeline to see the chain Gateway knows about. A header contains no transactions and does not mean the full block body is saved or that all transaction spending rules were checked.'
      ]
    },
    blocks: {
      title: 'Raw blocks',
      paragraphs: [
        'Keeps complete verified Bitcoin blocks, including transaction inputs, outputs and witness data. Use Raw when you want to reopen those blocks locally, including when a remote source is unavailable.',
        'Its coverage shows block bodies actually saved here. Other tracks keep their own calculated data and coverage; retaining Raw does not build those tracks.'
      ]
    },
    'tx-locator': {
      title: 'Transaction Locator',
      paragraphs: [
        'Remembers the block and transaction position for each transaction ID it has processed. Use it to find a transaction quickly when you have its long Bitcoin transaction ID.',
        'This is a location lookup, not a second copy of every transaction. It does not establish amounts, spending status or a balance. A known block-and-position address, called a blocator, already tells Gateway where to look.'
      ]
    },
    'txio-links': {
      title: 'Positional links',
      paragraphs: [
        'Keeps known links from a transaction input to the earlier output it spends, and from an output to a spending input. Use these links to follow money through transactions.',
        'A missing link means the spend is not established by the available data. It does not prove an output is unspent. Amounts and payment conditions have their own tracks and can be saved separately.'
      ]
    },
    'txio-values': {
      title: 'Values',
      paragraphs: [
        'Keeps the exact amount created in each saved transaction output, measured in sats. Use it to inspect an output’s amount after the complete source block is no longer saved.',
        'Values records known amounts. It does not tell you whether an output has been spent, and it does not establish a current balance. Its switch and saved coverage are separate from Positional links and ScriptPubKeys.'
      ]
    },
    'txio-scripts': {
      title: 'ScriptPubKeys',
      paragraphs: [
        'Keeps each output’s exact payment conditions, called its scriptPubKey. Use it to inspect how an output can be spent or to display its address when those conditions have a supported address form.',
        'Some outputs do not have a standard address. Saving their exact conditions still preserves useful evidence. This track does not establish whether an output is unspent; its switch and coverage are separate from Values and Positional links.'
      ]
    },
    'utxo-state': {
      title: 'UTXO',
      paragraphs: [
        'Keeps the complete set of outputs that are unspent at one saved Bitcoin block. UTXO means “unspent transaction output.” Use it to check which outputs are still available to spend at that stated point.',
        'The result describes its saved block, which may be behind the current chain tip. This state calculation is separate from saved spending links, historical payment totals and the identities of individual sats.',
        'Gateway reports the evidence it has checked; this index is not a claim that Gateway has performed every Bitcoin consensus and script check.'
      ]
    },
    'txio-sat-ranges': {
      title: 'Sat ranges',
      paragraphs: [
        'Tracks which individual sats belong to an output, using the order in which sats move through transactions. Use it to follow a particular sat or inspect the sat carrying an inscription.',
        'The Sats state records placement at its saved block. Keeping historical output ranges is a separate retention choice, so a current state does not imply that every older output’s ranges are saved.',
        'Sat identities answer a different question from amounts or unspent status. Each view reports the block and coverage its answer belongs to.'
      ]
    },
    'address-state': {
      title: 'Current Addresses',
      paragraphs: [
        'Groups the unspent outputs for a supported payment address or public-key destination at one saved block. Use it to inspect that address’s balance and individual unspent outputs at the stated point.',
        'A saved balance may be behind the chain tip. Historical received amounts, pending transactions and current inscription ownership are different questions and are not inferred from this view.'
      ]
    },
    'address-history': {
      title: 'Historical Outputs',
      paragraphs: [
        'Keeps outputs created for supported addresses or public-key destinations in the blocks you scan, including outputs that were later spent. Use it to investigate payments received during a chosen range.',
        'Each scanned block has its own coverage, including blocks with no matching outputs. Unscanned gaps remain unknown. Received totals over this history are not a current balance.',
        'An individual output’s supported address can also be derived directly from its payment conditions, without building this address history.'
      ]
    },
    inscriptions: {
      title: 'Inscriptions',
      paragraphs: [
        'Finds inscription reveals in transaction witness data and saves their positions. Lean keeps reveal locations and counts; Full also keeps reveal content. Use it to find an inscription’s reveal or retain content for later viewing.',
        'Each processed block has its own coverage. Reveal position and content do not by themselves establish an inscription’s canonical number, current owner or present location.'
      ]
    },
    'bitmap-districts-standard-v1': {
      title: 'Bitmap Districts',
      paragraphs: [
        'Keeps the selected District claim for each numbered Bitcoin block under the Bitmap rules shown in Rules. Use it to find the inscription selected for a block and inspect its reveal location.',
        'The claim result and its saved history are distinct from current inscription ownership. The exact Rules view governs claim selection; this help adds no eligibility conditions.'
      ]
    },
    'bitmap-parcels-standard-v1': {
      title: 'Bitmap Parcels',
      paragraphs: [
        'Keeps selected Parcel claim results under the Bitmap rules shown in Rules. Use it to inspect a parcel claim, its District and the referenced Bitcoin transaction position.',
        'An unresolved result means the evidence has not established an answer. It is not a completed empty result. The exact Rules view governs claim selection; this help adds no ownership or eligibility condition.'
      ]
    },
    'bitmap-chain': {
      title: 'Bitmap Chains',
      paragraphs: [
        'Bitmap Chains are disabled in this version while their method is being developed. Existing saved Chain evidence can be inspected; new Chain processing is unavailable.',
        'Their intended purpose is to summarize completed District or Parcel results in an ordered history. A summary alone does not establish that the underlying claims are correct or identify an inscription’s current owner.'
      ]
    },
    satline: {
      title: 'Sat path',
      paragraphs: [
        'Investigates the path of a selected sat or inscription through known transaction evidence. Use it to examine a particular transfer one step at a time.',
        'A stopped or unresolved path marks the limit of the available evidence. It does not establish a complete history or a current owner.'
      ]
    }
  };

  const sourceParagraph = 'These are source inputs, not instructions to retain another index. Gateway can read a complete block temporarily with Raw Off; saved Raw bodies are an optional way to make later reads available locally.';
  const bodyInputs = [
    {name: 'A complete verified Bitcoin block', requirement: 'input', why: 'Supplies the transaction data to read. It can come from an available local or remote block source.'},
    {name: 'The selected Bitcoin chain', requirement: 'input', why: 'Gateway manages the checked headers used to identify the expected block. This is managed chain information, not an extra index you must start.'}
  ];
  const locatorOptional = {name: 'Transaction Locator', requirement: 'optional', why: 'Helps find a transaction when only its Bitcoin transaction ID is known. A known block-and-position address can be read directly.'};
  const dependencyCards = {
    'group:blocks': {
      paragraphs: ['Headers are managed chain information. Raw uses an available complete block source to retain a verified body. Neither track automatically builds the derived tracks.'],
      dependencies: [{name: 'Bitcoin chain and block sources', requirement: 'input', why: 'Headers identify the chain; complete block sources provide the transactions Raw can save.'}]
    },
    'group:txio': {
      paragraphs: [sourceParagraph, 'The location, link, value and script tracks can learn from separate complete blocks. UTXO and Sats each maintain their own state in order from the first Bitcoin block; neither is a prerequisite for the other. See an individual dependency card for its exact requirements.'],
      dependencies: bodyInputs
    },
    'group:addresses': {
      paragraphs: ['Current Addresses requires one complete matching UTXO snapshot. Historical Outputs can scan separate complete blocks without building UTXO or Current Addresses.', sourceParagraph],
      dependencies: [{name: 'Matching UTXO snapshot — Current Addresses only', requirement: 'hard', otherTrack:true, why: 'Establishes which outputs are unspent at the exact block used for the address balance.'}, ...bodyInputs]
    },
    'group:bitmap': {
      paragraphs: [sourceParagraph, 'District and Parcel processing continues in order from Bitmap’s starting block, 792435. Parcels also use matching District results and the particular earlier evidence needed to check a candidate. Enabling a track never starts its prerequisites automatically. Chains remain disabled.'],
      dependencies: [{name: 'Matching District results — Parcels only', requirement: 'hard', otherTrack:true, why: 'Parcels use District results for the same Bitcoin block and rule set.'}, {name: 'Retained Sat evidence before a reveal — when needed', requirement: 'conditional', otherTrack:true, why: 'Some Parcel checks need the exact earlier Sat context. Turning Sats On now does not promise that historical context is retained.'}, ...bodyInputs]
    },
    headers: {
      paragraphs: ['Gateway obtains and checks headers as part of its managed Bitcoin chain information. Saving full blocks or building transaction tracks is a separate choice.'],
      dependencies: [{name: 'Bitcoin header source', requirement: 'input', why: 'Provides the headers Gateway checks to establish its selected chain.'}]
    },
    blocks: {paragraphs: [sourceParagraph], dependencies: bodyInputs},
    'tx-locator': {paragraphs: [sourceParagraph, 'No Positional links, Values, ScriptPubKeys, UTXO or Sats index is needed to save these transaction positions.'], dependencies: bodyInputs},
    'txio-links': {paragraphs: [sourceParagraph, 'Links can be saved independently. If the creation position of a spent output is still unknown, that part of the relationship stays unresolved.'], dependencies: [...bodyInputs, locatorOptional]},
    'txio-values': {paragraphs: [sourceParagraph, 'Values can be retained without enabling Positional links, ScriptPubKeys, UTXO or an Addresses index.'], dependencies: bodyInputs},
    'txio-scripts': {paragraphs: [sourceParagraph, 'ScriptPubKeys can be retained without enabling Positional links, Values, UTXO or an Addresses index. A supported address is derived from the script itself.'], dependencies: bodyInputs},
    'utxo-state': {
      paragraphs: [sourceParagraph, 'UTXO processes every block in order from the first Bitcoin block, or resumes its own checked saved state. A later block alone cannot establish the complete set. It does not require the Transaction Locator, Positional links or Sats indexes.'],
      dependencies: [{name: 'UTXO’s complete earlier state', requirement: 'hard', why: 'Each block removes spent outputs and adds new ones to the complete set. Missing earlier blocks cannot be skipped.'}, ...bodyInputs]
    },
    'txio-sat-ranges': {
      paragraphs: [sourceParagraph, 'Sats processes every block in order from the first Bitcoin block, or resumes its own checked saved state. It does not require UTXO, Positional links or the Transaction Locator.', 'Historical output ranges are saved only when their retention option is explicitly enabled. Turning it on later does not recreate older range history.'],
      dependencies: [{name: 'Sats’ complete earlier state', requirement: 'hard', why: 'Earlier output identities establish which sats enter each new transaction. Missing earlier context cannot be replaced by an isolated later block.'}, {name: 'Keep historical output ranges', requirement: 'hard', why: 'This explicit retention choice is needed to retain output range history in addition to the current Sats state.'}, ...bodyInputs]
    },
    'address-state': {
      paragraphs: ['Current Addresses reads one complete UTXO snapshot for the same selected Bitcoin block. A stale or differently anchored address result cannot supply a current balance.', 'Enabling Current Addresses does not start a UTXO build. Historical Outputs, the Transaction Locator and global Sats are not prerequisites.'],
      dependencies: [{name: 'A complete matching UTXO snapshot', requirement: 'hard', otherTrack:true, why: 'Provides the exact outputs still unspent at the block used by the address result.'}]
    },
    'address-history': {paragraphs: [sourceParagraph, 'Historical Outputs can scan separate blocks without UTXO, Current Addresses, Positional links or global Sats. It reads supported destinations directly from output scripts.'], dependencies: bodyInputs},
    inscriptions: {paragraphs: [sourceParagraph, 'Reading reveals does not require a Transaction Locator, UTXO, global Sats or a Bitmap index. Numbering or present ownership uses separate evidence and is not established by a reveal scan.'], dependencies: bodyInputs},
    'bitmap-districts-standard-v1': {
      paragraphs: [sourceParagraph, 'Fresh District processing starts at block 792435 and continues in order, or resumes its own checked saved result. Shared inscription reading does not require a separately retained Inscriptions index.'],
      dependencies: [{name: 'Earlier District results', requirement: 'hard', why: 'Consecutive evaluation from Bitmap’s starting block establishes the history used by later claim selections.'}, ...bodyInputs]
    },
    'bitmap-parcels-standard-v1': {
      paragraphs: [sourceParagraph, 'Parcel processing continues in order from block 792435, or resumes its own checked saved result. No prerequisite build starts automatically. A separately retained Inscriptions index is optional.', 'Merely turning Sats On does not establish the required historical state. If the exact earlier evidence is unavailable, or the parent moves within the same reveal block, the affected check remains unresolved.'],
      dependencies: [
        {name: 'Matching Bitmap District results', requirement: 'hard', otherTrack:true, why: 'Uses completed District results for the same Bitcoin block and the same declared rule set.'},
        {name: 'The referenced District block', requirement: 'input', why: 'Its verified transaction count establishes whether the Parcel’s transaction position exists.'},
        {name: 'Retained Sat evidence before the reveal', requirement: 'conditional', otherTrack:true, why: 'A candidate’s creation-time parent relationship is checked against the exact retained pre-reveal Sat context when that proof is needed. This is earlier relationship evidence, not a current-ownership condition.'},
        {name: 'Earlier Parcel results', requirement: 'hard', why: 'Consecutive evaluation preserves the Parcel track’s own completed history.'},
        ...bodyInputs
      ]
    },
    satline: {paragraphs: [sourceParagraph, 'A path can use available locations, links and retained evidence. Missing evidence stops that path; it does not automatically build a global index.'], dependencies: [...bodyInputs, locatorOptional]}
  };

  const aliases = {'group:transactions': 'group:txio', 'txo-spender': 'txio-links', 'sat-state': 'txio-sat-ranges'};
  const temporarySources = new Set(['group:txio', 'tx-locator', 'txio-links', 'txio-values', 'txio-scripts', 'utxo-state', 'txio-sat-ranges', 'address-history', 'inscriptions', 'bitmap-districts-standard-v1']);
  const ownContext = {
    'group:blocks': ['Headers are managed chain information. Raw can save complete blocks from an available local or remote source; it does not start derived indexing.'],
    headers: ['Gateway manages this checked header chain. Retaining full block bodies is a separate choice.'],
    blocks: ['With Raw Off, Gateway can still read verified blocks temporarily for other tracks. Raw coverage counts only the complete bodies actually retained here.'],
    'group:txio': ['UTXO and Sats each maintain their own state by processing blocks in order from the first Bitcoin block. Their own earlier state and history choices do not make either track a dependency of the other.'],
    'txio-links': ['An unknown creation position remains unresolved. The optional Transaction Locator can help when only a transaction ID is known; a known block-and-position address can be opened directly.'],
    'utxo-state': ['UTXO processes every block in order from the first Bitcoin block, or resumes its own checked saved state. An isolated later block cannot establish the complete unspent set.'],
    'txio-sat-ranges': ['Sats processes every block in order from the first Bitcoin block, or resumes its own checked saved state. This is separate from UTXO and Positional links.', 'Historical output ranges are saved only when their retention option is explicitly enabled. Turning it on later does not recreate older range history.'],
    'bitmap-districts-standard-v1': ['District processing starts at block 792435 and continues in order, or resumes its own checked saved result. Its shared inscription reading is separate from retaining the Inscriptions track.']
  };
  const legacy = new Set(['bitmap', 'bitmap-districts', 'bitmap-parcels', 'bitmap-districts-canonical', 'bitmap-parcels-canonical', 'bitmap-compatibility']);
  function key(id) { return typeof id === 'string' ? id.trim() : ''; }
  function isChain(id, definition) { return id === 'bitmap-chain' || /^bitmap-(?:district|parcel)-chain(?:-|$)/.test(id) || definition?.kind === 'chain'; }
  function frozenBitmap(id, definition) { return legacy.has(id) || id.startsWith('bitmap-') && !purposes[id] && !isChain(id, definition); }
  function name(definition, fallback) { return typeof definition?.name === 'string' && definition.name.trim() ? definition.name.trim() : fallback; }
  function copy(card, title) {
    return {title: title || card.title, paragraphs: [...card.paragraphs], ...(card.dependencies ? {dependencies: card.dependencies.map(row => ({...row}))} : {})};
  }
  function get(id, definition) {
    id = key(id); id = aliases[id] || id;
    if (isChain(id, definition)) return copy(purposes['bitmap-chain']);
    if (purposes[id]) {
      const card = copy(purposes[id]);
      if (temporarySources.has(id)) card.paragraphs.push('Gateway can read complete verified blocks temporarily with Raw Off. Retaining a Raw copy is a separate choice; each track keeps its own saved facts and coverage.');
      card.paragraphs.push(...(ownContext[id] || []));
      return card;
    }
    const title = name(definition, 'Saved track');
    if (frozenBitmap(id, definition)) return {title, paragraphs: ['This saved definition is available for inspection only in this version. Use it to inspect its retained results and their own recorded coverage.', 'Earlier or custom Bitmap results keep their own saved interpretation. They are not silently replaced with the current District or Parcel rules.']};
    if (definition?.locked === true || definition?.buildable === false) return {title, paragraphs: ['This saved definition is available for inspection only in this version. Use it to inspect its retained results and their own recorded coverage.', 'Opening help does not enable processing or change the saved definition.']};
    return {title, paragraphs: ['This track’s saved definition describes the data it records. Inspect its details and reported coverage to see which results are available.', 'A track’s position in a group does not establish that it contains another track’s data. Missing coverage remains unknown.']};
  }
  function dependencies(id, definition) {
    id = key(id); id = aliases[id] || id;
    if (isChain(id, definition)) return null;
    if (dependencyCards[id]) {
      const card = copy(dependencyCards[id], (purposes[id]?.title || name(definition, 'Track')) + ' dependencies');
      card.showIndicator = card.dependencies.some(row => row.otherTrack === true && ['hard', 'conditional'].includes(row.requirement));
      return card;
    }
    if (frozenBitmap(id, definition) || definition?.locked === true || definition?.buildable === false) return {title: 'Saved definition', paragraphs: ['This definition is available for inspection only. Building another index cannot enable new processing for it in this version.'], dependencies: [], showIndicator:false};
    return {title: 'Dependencies not established', paragraphs: ['The requirements for this definition are not established by this help. Inspect its saved definition; its group and catalog source labels do not prove that another retained index must be built.'], dependencies: [], showIndicator:false};
  }
  return Object.freeze({get, dependencies});
});
