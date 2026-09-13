import antfu from '@antfu/eslint-config'

export default antfu(
  {
    react: true,
    typescript: true,
    ignores: ['src/app/routeTree.gen.ts', 'coverage/**'],
  },
  {
    // Route modules export `Route` next to their component (the router plugin splits them);
    // shadcn primitives export variant helpers next to components.
    files: ['src/app/routes/**/*.tsx', 'src/shared/components/ui/**/*.tsx'],
    rules: { 'react-refresh/only-export-components': 'off' },
  },
)
