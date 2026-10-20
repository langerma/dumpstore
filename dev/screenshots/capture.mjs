// capture.mjs — drive the deployed dev VM in headless Chrome and write the
// README/wiki screenshots to screenshots/. Run via `make screenshots`, which
// seeds demo data first (seed.sh) and removes it afterwards.
//
// Env: DUMPSTORE_URL (default http://localhost:8080), DUMPSTORE_USER/PASS
// (admin/admin), CHROME_PATH (default: Google Chrome on macOS), OUT_DIR.
import puppeteer from 'puppeteer-core';
import { mkdir } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const URL = process.env.DUMPSTORE_URL || 'http://localhost:8080';
const OUT = process.env.OUT_DIR || path.resolve(here, '../../screenshots');
const CHROME = process.env.CHROME_PATH
  || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const sleep = ms => new Promise(r => setTimeout(r, ms));

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: true,
  defaultViewport: { width: 1440, height: 900, deviceScaleFactor: 1 },
});
const page = await browser.newPage();
await mkdir(OUT, { recursive: true });

// ── helpers ──────────────────────────────────────────────────────────────────
const file = name => path.join(OUT, name);

async function tab(name) {
  await page.click(`.tab-btn[data-tab="${name}"]`);
  await sleep(1200);
  await page.evaluate(() => window.scrollTo(0, 0));
}

// Click a delegated row/drawer action, e.g. act('nfs', { ds: 'tank/media' }).
async function act(action, attrs = {}, scope = 'body') {
  const sel = `${scope} [data-action="${action}"]` +
    Object.entries(attrs).map(([k, v]) => `[data-${k}="${v}"]`).join('');
  await page.waitForSelector(sel);
  await page.$eval(sel, el => el.click());
}

async function shotPage(name) {
  await page.screenshot({ path: file(name), fullPage: true });
  console.log('  ', name);
}

async function shotViewport(name) {
  await page.screenshot({ path: file(name) });
  console.log('  ', name);
}

// Dialogs are slide-in side panels: capture the window with the panel open,
// as the user sees it, then close it.
async function shotDialog(id, name, settleMs = 700) {
  await page.waitForSelector(`#${id}[open]`);
  await sleep(settleMs);
  await page.screenshot({ path: file(name) });
  console.log('  ', name);
  await page.$eval(`#${id}`, d => d.close());
  await sleep(200);
}

async function closeDrawer() {
  if (await page.$('#dataset-drawer [data-action="close"]')) {
    await page.$eval('#dataset-drawer [data-action="close"]', el => el.click());
    await sleep(400);
  }
}

// ── login ────────────────────────────────────────────────────────────────────
await page.goto(`${URL}/login`, { waitUntil: 'networkidle2' });
// The login page pre-fills the configured username — set values, don't type.
await page.$eval('input[name="username"]', (el, v) => { el.value = v; }, process.env.DUMPSTORE_USER || 'admin');
await page.$eval('input[name="password"]', (el, v) => { el.value = v; }, process.env.DUMPSTORE_PASS || 'admin');
await page.focus('input[name="password"]');
await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle2' }), page.keyboard.press('Enter')]);
await sleep(4000); // initial load + slow metrics (iostat, SMART)

// ── Sysinfo / pools ──────────────────────────────────────────────────────────
console.log('pools');
await tab('pools');
await sleep(2000);
await shotPage('sysinfo.png');
await page.click('#createPoolBtn');
await shotDialog('createPoolDialog', 'pool-create.png', 1500);

// ── Datasets ─────────────────────────────────────────────────────────────────
console.log('datasets');
await tab('datasets');
await page.hover('[data-action="open"][data-ds="tank/documents"]');
await shotPage('datasets.png');

await page.click('#newDatasetBtn');
await shotDialog('newDatasetDialog', 'dataset-create.png');

await act('open', { ds: 'tank/documents' });
await sleep(1500);
await shotViewport('dataset-drawer.png');

await act('acl', {}, '#dataset-drawer');
await shotDialog('aclDialog', 'acl.png', 1200);
await act('usage', {}, '#dataset-drawer');
await shotDialog('userSpaceDialog', 'user-quotas.png', 1500);
await closeDrawer();

await act('nfs', { ds: 'tank/documents' });
await shotDialog('nfsDialog', 'nfs.png');
await act('smb', { ds: 'tank/media' });
await shotDialog('smbDialog', 'smb-share.png');
await act('iscsi', { ds: 'tank/vms/win11' });
await shotDialog('iscsiDialog', 'iscsi.png', 1000);
await act('autosnap', { ds: 'tank/media' });
await shotDialog('autoSnapDialog', 'auto-snapshot.png', 1000);

// ── Snapshots ────────────────────────────────────────────────────────────────
console.log('snapshots');
await tab('snapshots');
// The VM's own zfs-auto-snapshot fills tank and tank/data with dozens of
// snapshots; fold those groups so the demo datasets stay in view.
for (const ds of ['tank', 'tank/data']) {
  await act('collapse-group', { dataset: ds });
  await sleep(400);
}
await shotPage('snapshots.png');

await act('diff', { snap: 'tank/documents@before-cleanup' });
await page.waitForSelector('#diffSnapDialog[open]');
await page.$eval('#diffSnapRunBtn', el => el.click());
await sleep(2500);
await shotDialog('diffSnapDialog', 'snapshot-diff.png', 300);

await act('send', { snap: 'tank/documents@after-cleanup' });
await shotDialog('sendSnapDialog', 'snapshot-send.png');
await act('del', { snap: 'tank/homes/alice@pre-upgrade' });
await shotDialog('deleteSnapDialog', 'delete-snapshot.png');

// ── Users & Groups, Samba ────────────────────────────────────────────────────
console.log('users');
await tab('users');
await sleep(1000);
// Lima mirrors the host account into the VM; keep it out of the pictures.
await page.evaluate(name => {
  for (const td of document.querySelectorAll('#tab-users td:first-child')) {
    if (td.textContent.trim() === name) td.closest('tr').style.display = 'none';
  }
}, process.env.HIDE_ACCOUNT || os.userInfo().username);
await shotPage('users-groups.png');
await page.click('#newUserBtn');
await shotDialog('newUserDialog', 'create-user.png');
await page.click('#tmAddBtn');
await shotDialog('timeMachineDialog', 'time-machine.png');

// ── Services, replication, jobs ──────────────────────────────────────────────
console.log('services / replication / jobs');
await tab('services');
await shotPage('services.png');
await tab('replication');
await shotPage('replication.png');
await page.click('#newReplBtn');
await shotDialog('replDialog', 'replication-task.png');
await tab('jobs');
await shotPage('jobs.png');

await browser.close();
console.log(`done → ${OUT}`);
