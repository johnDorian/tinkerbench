import { mount } from 'svelte'
import '@fontsource-variable/ibm-plex-sans'
import '@fontsource-variable/jetbrains-mono'
import '$shared/theme.css'
import './app.css'
import App from './App.svelte'

mount(App, { target: document.getElementById('app') })
