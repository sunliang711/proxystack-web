import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'

/** 代码块语法高亮：只注册页面上用到的 shell，控制打包体积。 */
hljs.registerLanguage('bash', bash)

export { hljs }
