import fastGlob from 'fast-glob';
import wrapAnsi from 'wrap-ansi';
import {init as licenseChecker} from 'license-checker-rseidelsohn';
import {VueLoaderPlugin} from 'vue-loader';
import {EsbuildPlugin} from 'esbuild-loader';
import {parse} from 'node:path';
import webpack, {type Configuration} from 'webpack';
import {fileURLToPath} from 'node:url';
import {readFileSync, writeFileSync} from 'node:fs';
import {env} from 'node:process';
import tailwindcss from 'tailwindcss';
import tailwindConfig from './tailwind.config.ts';
import tailwindcssNesting from 'tailwindcss/nesting/index.js';
import postcssNesting from 'postcss-nesting';
import goJson from './assets/go-licenses.json' with {type: 'json'};

const {SourceMapDevToolPlugin, DefinePlugin, ProgressPlugin, ProvidePlugin} = webpack;

const formatLicenseText = (licenseText: string) => wrapAnsi(licenseText || '', 80).trim();

const baseDirectory = import.meta.dirname;
const glob = (pattern: string): string[] => fastGlob.sync(pattern, {
  cwd: baseDirectory,
  absolute: true,
});

const themes: Record<string, [string]> = {};
for (const path of glob('web_src/css/themes/*.css')) {
  themes[parse(path).name] = [path];
}

const isProduction = env.NODE_ENV !== 'development';

if (isProduction) {
  licenseChecker({
    start: baseDirectory,
    production: true,
    onlyAllow: 'Apache-2.0; 0BSD; BSD-2-Clause; BSD-3-Clause; BlueOak-1.0.0; MIT; ISC; Unlicense; CC-BY-4.0; GPL-3.0-or-later',
    // argparse@2.0.1 - Python-2.0. It's used in the CLI file of markdown-it and js-yaml and not in the library code.
    // idiomorph@0.3.0. See https://github.com/bigskysoftware/idiomorph/pull/37
    excludePackages: 'argparse@2.0.1;idiomorph@0.3.0',
  }, (err, dependencies) => {
    if (err) {
      throw err;
    }

    const line = '-'.repeat(80);
    const goModules = goJson.map(({name, licenseText}) => {
      return {name, licenseName: '', body: formatLicenseText(licenseText)};
    });
    const jsModules = Object.keys(dependencies).map((packageName) => {
      const {licenses, licenseFile} = dependencies[packageName];
      const licenseText = (licenseFile && !licenseFile?.toLowerCase().includes('readme')) ? readFileSync(licenseFile, 'utf-8') : '[no license file]';
      return {name: packageName, licenseName: licenses, body: formatLicenseText(licenseText)};
    });
    const modules = [...goModules, ...jsModules];
    const licenseTxt = modules.map(({name, licenseName, body}) => {
      const title = licenseName ? `${name} - ${licenseName}` : name;
      return `${line}\n${title}\n${line}\n${body}`;
    }).join('\n');
    writeFileSync('public/assets/licenses.txt', licenseTxt);
  });
} else {
  writeFileSync('public/assets/licenses.txt', 'Licenses are disabled during development');
}

// ENABLE_SOURCEMAP accepts the following values:
// true - all enabled, the default in development
// reduced - minimal sourcemaps, the default in production
// false - all disabled
let sourceMaps: string;
if ('ENABLE_SOURCEMAP' in env) {
  sourceMaps = ['true', 'false'].includes(env.ENABLE_SOURCEMAP) ? env.ENABLE_SOURCEMAP : 'reduced';
} else {
  sourceMaps = isProduction ? 'reduced' : 'true';
}

// define which web components we use for Vue to not interpret them as Vue components
const webComponents = new Set([
  // our own, in web_src/js/webcomponents
  'i18n',
  'overflow-menu',
  'origin-url',
  'absolute-date',
  'relative-time',
  // from dependencies
  'markdown-toolbar',
  'text-expander',
]);

/** See <https://esbuild.github.io/content-types/#javascript> for what esbuild does here. */
const esbuildJsTarget = 'es2020'; // TODO: es2025?

export default {
  experiments: {
    css: true,
  },
  mode: isProduction ? 'production' : 'development',
  entry: {
    index: [
      fileURLToPath(new URL('web_src/js/jquery.js', import.meta.url)),
      fileURLToPath(new URL('web_src/fomantic/build/semantic.js', import.meta.url)),
      fileURLToPath(new URL('web_src/js/index.js', import.meta.url)),
      fileURLToPath(new URL('node_modules/easymde/dist/easymde.min.css', import.meta.url)),
      fileURLToPath(new URL('web_src/fomantic/build/semantic.css', import.meta.url)),
      fileURLToPath(new URL('web_src/css/index.css', import.meta.url)),
    ],
    webcomponents: [
      fileURLToPath(new URL('web_src/js/webcomponents/index.ts', import.meta.url)),
    ],
    forgejoswagger: [ // Forgejo swagger is OpenAPI 3.0.0 and has specific parameters
      fileURLToPath(new URL('web_src/js/standalone/forgejo-swagger.js', import.meta.url)),
      fileURLToPath(new URL('web_src/css/standalone/swagger.css', import.meta.url)),
    ],
    swagger: [
      fileURLToPath(new URL('web_src/js/standalone/swagger.js', import.meta.url)),
      fileURLToPath(new URL('web_src/css/standalone/swagger.css', import.meta.url)),
    ],
    'eventsource.sharedworker': [
      fileURLToPath(new URL('web_src/js/features/eventsource.sharedworker.js', import.meta.url)),
    ],
    ...(!isProduction && {
      demo: [
        fileURLToPath(new URL('web_src/js/standalone/demo.ts', import.meta.url)),
        fileURLToPath(new URL('web_src/css/standalone/demo.css', import.meta.url)),
      ],
    }),
    ...themes,
  },
  devtool: false,
  output: {
    cssFilename: 'css/[name].css',
    cssChunkFilename: 'css/[name].[contenthash:8].css',
    path: fileURLToPath(new URL('public/assets', import.meta.url)),
    filename: () => 'js/[name].js',
    chunkFilename: () => {
      return `js/[name].[contenthash:8].js`;
    },
  },
  optimization: {
    minimize: isProduction,
    minimizer: [
      new EsbuildPlugin({
        target: esbuildJsTarget,
        minify: true,
        css: true,
        legalComments: 'none',
      }),
    ],
    splitChunks: {
      chunks: 'async',
      name: (_, chunks) => chunks.map((item) => item.name).join('-'),
    },
    moduleIds: 'named',
    chunkIds: 'named',
  },
  module: {
    rules: [
      {
        test: /\.vue$/i,
        exclude: /node_modules/,
        loader: 'vue-loader',
        options: {
          compilerOptions: {
            isCustomElement: (tag: string) => webComponents.has(tag),
          },
          experimentalInlineMatchResource: true,
        },
      },
      {
        test: /\.(js|ts)$/i,
        exclude: /node_modules/,
        use: [
          {
            loader: 'esbuild-loader',
            options: {
              loader: 'ts',
              target: esbuildJsTarget,
            },
          },
        ],
      },
      {
        test: /\.css$/i,
        use: [{
          loader: 'postcss-loader',
          options: {
            postcssOptions: {
              plugins: [
                tailwindcssNesting(postcssNesting({edition: '2024-02'})),
                tailwindcss(tailwindConfig),
              ],
            },
          },
        }],
        type: 'css/auto',
      },
      {
        test: /\.svg$/i,
        include: fileURLToPath(new URL('public/assets/img/svg', import.meta.url)),
        type: 'asset/source',
      },
      {
        test: /\.(ttf|woff2?)$/i,
        type: 'asset/resource',
        generator: {
          filename: 'fonts/[name].[contenthash:8][ext]',
        },
      },
    ],
  },
  plugins: [
    new ProgressPlugin({
      activeModules: true,
    }),
    new ProvidePlugin({ // for htmx extensions
      htmx: ['htmx.org', 'default'],
    }),
    new DefinePlugin({
      __VUE_OPTIONS_API__: true, // at the moment, many Vue components still use the Vue Options API
      __VUE_PROD_DEVTOOLS__: false, // do not enable devtools support in production
      __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: false, // https://github.com/vuejs/vue-cli/pull/7443
    }),
    new VueLoaderPlugin(),
    sourceMaps !== 'false' && new SourceMapDevToolPlugin({
      filename: '[file].[contenthash:8].map',
      ...(sourceMaps === 'reduced' && {include: /^js\/index\.js$/}),
    }),
  ],
  performance: {
    hints: false,
    maxEntrypointSize: Infinity,
    maxAssetSize: Infinity,
  },
  resolve: {
    symlinks: false,
  },
  watchOptions: {
    ignored: [
      'node_modules/**',
    ],
  },
  stats: {
    assetsSort: 'name',
    assetsSpace: Infinity,
    cached: false,
    cachedModules: false,
    children: false,
    chunkModules: false,
    chunkOrigins: false,
    chunksSort: 'name',
    colors: true,
    entrypoints: false,
    excludeAssets: [
      !isProduction && /^licenses.txt$/,
    ].filter(Boolean),
    groupAssetsByChunk: false,
    groupAssetsByEmitStatus: false,
    groupAssetsByInfo: false,
    groupModulesByAttributes: false,
    modules: false,
    reasons: false,
    runtimeModules: false,
  },
} satisfies Configuration;
