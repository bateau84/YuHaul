import { pathToFileURL } from 'node:url';
import createPreset from 'conventional-changelog-conventionalcommits';
import { CommitParser } from 'conventional-commits-parser';

export async function checkTitle(title) {
  const preset = await createPreset();
  const parsed = new CommitParser(preset.parser).parse(title);
  const types = ['feat', 'fix', 'perf', 'revert', 'docs', 'style', 'chore', 'refactor', 'test', 'build', 'ci'];
  if (typeof title !== 'string' || /[\r\n]/.test(title) ||
      !types.includes(parsed.type) || !parsed.subject?.trim()) {
    throw new Error('PR title must be a Conventional Commit: type(scope)!: description (scope and ! optional)');
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await checkTitle(process.env.PR_TITLE ?? '');
}
