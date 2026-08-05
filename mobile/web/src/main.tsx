import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { registerBuiltins } from './extensions/builtins'
import { loadCustomExtensions } from './extensions/registry'
import './styles.css'

registerBuiltins()
void loadCustomExtensions()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
