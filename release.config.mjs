export const conventionalOptions = { preset: 'conventionalcommits' };

export default {
  branches: ['main'],
  tagFormat: 'v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', conventionalOptions],
    ['@semantic-release/release-notes-generator', conventionalOptions],
    ['@semantic-release/github', {
      successComment: false,
      failComment: false,
      failTitle: false,
      releasedLabels: false,
    }],
  ],
};
