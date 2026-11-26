import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import asyncImageTasks from './asyncImageTasks'
import asyncImageRetryRules from './asyncImageRetryRules'
import imageWorkflow from './imageWorkflow'
import imageCleanup from './imageCleanup'
import admin from './admin'
import misc from './misc'

export default {
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  ...asyncImageTasks,
  ...asyncImageRetryRules,
  ...imageWorkflow,
  ...imageCleanup,
  admin,
  ...misc,
}
