import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import semanticRelease from 'semantic-release';

export function releaseOutputs(result, expectedHead) {
  if (!result) return { published: 'false' };
  const { version, gitTag, gitHead } = result.nextRelease;
  if (!result.releases.some(release => release.pluginName === '@semantic-release/github')) {
    throw new Error('GitHub publication did not succeed');
  }
  if (version.trim() !== version || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version) || gitTag !== `v${version}` ||
      !/^[a-f0-9]{40}$/.test(gitHead) || gitHead !== expectedHead) {
    throw new Error('Release identity does not match the verified source');
  }
  return { published: 'true', version, git_tag: gitTag, git_head: gitHead };
}

export async function runRelease({ release = semanticRelease, env = process.env, write = appendFileSync } = {}) {
  if (env.GITHUB_EVENT_NAME !== 'push' || env.GITHUB_REF !== 'refs/heads/main' ||
      !/^[a-f0-9]{40}$/.test(env.GITHUB_SHA ?? '') || !env.GITHUB_OUTPUT) {
    throw new Error('Publication requires a verified main push in GitHub Actions');
  }
  // Never expose dry-run output as a successful publication.
  const result = await release({ dryRun: false, ci: true });
  const outputs = releaseOutputs(result, env.GITHUB_SHA);
  write(env.GITHUB_OUTPUT, Object.entries(outputs).map(([key, value]) => `${key}=${value}\n`).join(''));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await runRelease();
}
