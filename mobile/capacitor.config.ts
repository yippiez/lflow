import type { CapacitorConfig } from '@capacitor/cli'

// Android wrapper: the web app ships inside the APK; on first launch the app
// asks for the lflow server URL (Settings → Server) and talks to it over the
// network, exactly like the browser build.
const config: CapacitorConfig = {
  appId: 'app.lflow.mobile',
  appName: 'lflow',
  webDir: '../pkg/tui/daemon/webui/dist',
  server: {
    androidScheme: 'https',
  },
}

export default config
