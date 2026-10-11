'use strict';
const assert = require('node:assert/strict');

// Exercise the public pointer controls. This replaces the removed local jump
// form in fixtures without setting private controller state or changing ranges
// through global Explorer navigation.
async function selectHeight(page, height, index = null) {
  if (await page.locator('#timeline-expand').isVisible()) await page.locator('#timeline-expand').click();
  if (index) await page.locator('.timeline-track[data-index="'+index+'"] .timeline-track-select').click();
  const root = page.locator('#index-timeline');
  const view = () => root.evaluate(el => ({from:Number(el.dataset.from),to:Number(el.dataset.to)}));
  const lane = index ? page.locator('.timeline-lane[data-index="'+index+'"]') : page.locator('.timeline-lane[data-index="'+(await page.locator('#timeline-inspector').getAttribute('data-index'))+'"]');
  await page.locator('#timeline-fit').click();
  for (let i=0; i<12; i++) {
    const current=await view(), span=current.to-current.from+1;
    assert(height>=current.from&&height<=current.to,'Target remains inside pointer-anchored zoom');
    if (span<=33) break;
    const box=await lane.boundingBox();
    await page.mouse.move(box.x+(height-current.from+.5)/span*box.width,box.y+Math.min(25,box.height/2));
    await page.mouse.wheel(0,-300);
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  }
  while ((await view()).to-(await view()).from+1<33) {
    await lane.focus();
    await page.keyboard.press('-');
  }
  const current=await view(), box=await lane.boundingBox();
  assert(current.to-current.from<64,'Pointer zoom reaches individual block granularity');
  const x=box.x+(height-current.from+.5)/(current.to-current.from+1)*box.width,y=box.y+Math.min(25,box.height/2);
  // Indexing selection is explicit. Ctrl establishes this height as the anchor,
  // then Shift selects the singleton even if prior discontiguous picks exist.
  // The final ordinary click explores it without altering that selection.
  await page.keyboard.down('Control');await page.mouse.click(x,y);await page.keyboard.up('Control');
  await page.keyboard.down('Shift');await page.mouse.click(x,y);await page.keyboard.up('Shift');
  await page.mouse.click(x,y);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
}

module.exports={selectHeight};
