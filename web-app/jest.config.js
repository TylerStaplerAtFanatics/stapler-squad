module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'jsdom',
  roots: ['<rootDir>/src'],
  testMatch: ['**/__tests__/**/*.ts?(x)', '**/?(*.)+(spec|test).ts?(x)'],
  // tsconfig.json sets jsx:"preserve" (Next.js's own bundler normally does the
  // JSX transform downstream); ts-jest has no such downstream step, so JSX-
  // bearing .tsx test files need this override to actually compile JSX.
  transform: {
    '^.+\\.tsx?$': ['ts-jest', { tsconfig: { jsx: 'react-jsx' } }],
  },
  moduleNameMapper: {
    '^@/(.*)$': '<rootDir>/src/$1',
    // XtermTerminal.tsx imports xterm's CSS and a CSS module; jsdom/ts-jest
    // have no CSS transform registered, so stub both out with an empty object.
    '\\.(css|less|scss|sass)$': '<rootDir>/jest.styleMock.js',
  },
  setupFilesAfterEnv: ['<rootDir>/jest.setup.js'],
};
