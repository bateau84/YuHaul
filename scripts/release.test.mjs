import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { Writable } from 'node:stream';
import test from 'node:test';
import semanticRelease from 'semantic-release';
import { analyzeCommits } from '@semantic-release/commit-analyzer';
import { generateNotes } from '@semantic-release/release-notes-generator';
import config, { conventionalOptions } from '../release.config.mjs';
import { checkTitle } from './check-pr-title.mjs';
import { releaseOutputs, runRelease } from './release.mjs';

const logger = { log() {}, error() {} };
const sha = 'a'.repeat(40);
const published = {
  nextRelease: { version: '1.2.3', gitTag: 'v1.2.3', gitHead: sha },
  releases: [{ pluginName: '@semantic-release/github' }],
};

test('locked analyzer and notes share Conventional Commits breaking grammar', async () => {
  for (const [message, expected] of [
    ['feat: add routing', 'minor'], ['fix: correct routing', 'patch'],
    ['perf: reduce work', 'patch'], ['feat!: remove old API', 'major'],
    ['fix(api)!: replace contract', 'major'],
    ['chore: replace schema\n\nBREAKING CHANGE: old schema is unsupported', 'major'],
    ['docs: explain setup', null], ['ci: add pipeline', null],
    ['chore: maintain tooling', null], ['first commit', null],
  ]) {
    assert.equal(await analyzeCommits(conventionalOptions, { commits: [{ message, hash: sha }], logger }), expected, message);
  }
  assert.equal(await analyzeCommits(conventionalOptions, {
    commits: [{ message: 'fix: patch' }, { message: 'first commit' }, { message: 'feat: add feature' }], logger,
  }), 'minor');
  const notes = await generateNotes(conventionalOptions, {
    commits: [{ message: 'feat!: replace API', hash: sha }], logger,
    lastRelease: {}, nextRelease: { version: '1.0.0', gitTag: 'v1.0.0' },
    options: { repositoryUrl: 'https://github.com/example/project.git' },
  });
  assert.match(notes, /BREAKING CHANGES/);
  assert.match(notes, /replace API/);
  const original = { message: 'feat: add feature', hash: sha };
  const revert = { message: `revert: feat: add feature\n\nThis reverts commit ${sha}.`, hash: 'b'.repeat(40) };
  // Git history is newest first, as supplied by semantic-release.
  assert.equal(await analyzeCommits(conventionalOptions, { commits: [revert, original], logger }), null);
  assert.equal(await analyzeCommits(conventionalOptions, { commits: [revert], logger }), 'patch');
});

test('PR title grammar treats shell-looking subject as data', async () => {
  for (const title of ['feat: routing', 'fix(api)!: replace API', 'docs: $(touch /not-executed)', 'ci: `false`']) {
    await checkTitle(title);
  }
  for (const title of ['legacy title', 'feat: ', 'feat: one\nfix: two', 'unknown: change']) {
    await assert.rejects(checkTitle(title));
  }
});

test('handoff is emitted only after successful publisher and identity checks', async () => {
  assert.deepEqual(releaseOutputs(false, sha), { published: 'false' });
  assert.equal(releaseOutputs(published, sha).published, 'true');
  assert.throws(() => releaseOutputs({ ...published, releases: [] }, sha));
  assert.throws(() => releaseOutputs(published, 'b'.repeat(40)));
  for (const nextRelease of [
    { ...published.nextRelease, version: '1.2.3\npublished=true' },
    { ...published.nextRelease, version: '1.2.3\n', gitTag: 'v1.2.3\n' },
    { ...published.nextRelease, version: '01.2.3', gitTag: 'v01.2.3' },
    { ...published.nextRelease, gitTag: 'v9.0.0' },
    { ...published.nextRelease, gitHead: 'main' },
  ]) assert.throws(() => releaseOutputs({ ...published, nextRelease }, sha));
  const env = { GITHUB_EVENT_NAME: 'push', GITHUB_REF: 'refs/heads/main', GITHUB_SHA: sha, GITHUB_OUTPUT: 'output' };
  const writes = [];
  await runRelease({ env, release: async options => { assert.equal(options.dryRun, false); return published; }, write: (...args) => writes.push(args) });
  assert.match(writes[0][1], /^published=true\nversion=1.2.3\ngit_tag=v1.2.3\ngit_head=/);
  writes.length = 0;
  await assert.rejects(runRelease({ env, release: async () => { throw new Error('publisher failed'); }, write: (...args) => writes.push(args) }));
  assert.equal(writes.length, 0);
  await runRelease({ env, release: async () => false, write: (...args) => writes.push(args) });
  assert.equal(writes[0][1], 'published=false\n');
  for (const extra of [{ GITHUB_EVENT_NAME: 'pull_request' }, { GITHUB_REF: 'refs/heads/anchor' }, { GITHUB_REF: 'refs/tags/v1.0.0' }]) {
    await assert.rejects(runRelease({ env: { ...env, ...extra }, release: async () => assert.fail('must not call publisher') }));
  }
});

test('real semantic-release dry run computes native first 1.0.0 without a baseline tag', async () => {
  // Synthetic commits exist only in a disposable fixture; no project refs change.
  const cwd = mkdtempSync(join(tmpdir(), 'yuhaul-release-'));
  const git = (...args) => execFileSync('git', args, { cwd, stdio: 'pipe' }).toString().trim();
  const sink = new Writable({ write(_chunk, _encoding, callback) { callback(); } });
  try {
    git('init', '-b', 'main');
    git('config', 'user.name', 'Release test');
    git('config', 'user.email', 'release-test@example.invalid');
    git('commit', '--allow-empty', '-m', 'first commit');
    git('commit', '--allow-empty', '-m', 'feat: initial implementation');
    git('commit', '--allow-empty', '-m', 'ci: enable releases');
    const head = git('rev-parse', 'HEAD');
    const result = await semanticRelease({
      ...config,
      repositoryUrl: `file://${cwd}`,
      dryRun: true, ci: false,
      plugins: [
        { analyzeCommits: (options, context) => analyzeCommits(conventionalOptions, context) },
        { generateNotes: (options, context) => generateNotes(conventionalOptions, {
          ...context, options: { ...context.options, repositoryUrl: 'https://github.com/example/project.git' },
        }) },
      ],
    }, { cwd, env: { PATH: process.env.PATH, HOME: process.env.HOME }, stdout: sink, stderr: sink });
    assert.equal(result.nextRelease.version, '1.0.0');
    assert.equal(result.nextRelease.gitTag, 'v1.0.0');
    assert.equal(result.nextRelease.gitHead, head);
    assert.equal(git('tag'), '');
    assert.throws(() => releaseOutputs(result, head), /GitHub publication/);
  } finally {
    rmSync(cwd, { recursive: true, force: true });
  }
});
