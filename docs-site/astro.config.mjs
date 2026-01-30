// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	integrations: [
		starlight({
			title: 'Scion',
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/google/scion' },
			],
			sidebar: [
				{
					label: 'Start Here',
					items: [
						{ label: 'Overview', slug: 'overview' },
						{ label: 'Installation', slug: 'install' },
						{ label: 'Concepts', slug: 'concepts' },
						{ label: 'Settings', slug: 'settings' },
						{ label: 'Supported Harnesses', slug: 'supported-harnesses' },
					],
				},
				{
					label: 'Guides',
					autogenerate: { directory: 'guides' },
				},
				{
					label: 'Reference',
					autogenerate: { directory: 'reference' },
				},
			],
		}),
	],
});
