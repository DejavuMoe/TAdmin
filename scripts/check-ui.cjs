// Dev-only browser check. Run with the opt-in Go fixture; see docs/verification.md.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const file = process.env.TADMIN_UI_FIXTURE;
if (!file) throw new Error('Set TADMIN_UI_FIXTURE to the Go fixture output outside the checkout');
const fixture = JSON.parse(fs.readFileSync(file, 'utf8'));
const output = path.dirname(file);
(async () => {
 const browser = await chromium.launch({headless:true, executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined});
 const errors = [];
 try {
  for (const [name, size] of [['success',{width:1000,height:850}],['failure',{width:390,height:844}],['mobile',{width:320,height:740}]]) {
   const context=await browser.newContext({viewport:size,colorScheme:name==='mobile'?'dark':'light'});
   const page=await context.newPage();
   page.on('pageerror',e=>errors.push(e.message));
   page.on('console',m=>{if(['warning','error'].includes(m.type())) errors.push(m.text());});
   await page.route('**/favicon.ico',r=>r.fulfill({status:204}));
   const f=fixture[name];
   await page.route('https://telegram.org/js/telegram-web-app.js',route=>route.fulfill({contentType:'text/javascript',body:`window.Telegram={WebApp:{initData:${JSON.stringify(f.initData)},ready(){},expand(){}}};${name==='mobile'?'document.documentElement.style.setProperty("--tg-theme-bg-color","#17212b");document.documentElement.style.setProperty("--tg-theme-text-color","#f5f5f5");document.documentElement.style.setProperty("--tg-theme-hint-color","#a8b3bd");':''}`}));
   // One hour of client wall-clock skew must not expire a valid server challenge.
   await page.addInitScript(()=>{Date.now=()=>new Date().getTime()+3600000;});
   await page.goto(`${fixture.baseURL}/verify/${f.token}`);
   assert.equal(await page.title(),'TAdmin');
   await page.locator('.captcha-cells button:enabled').first().waitFor();
   assert.equal(await page.locator('.captcha-cells button').count(),9);
   assert.match(await page.locator('#time-value').innerText(),/^0[0-3]:[0-5]\d$/);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   await page.screenshot({path:path.join(output,`${name}-ready.png`)});
   if(name==='failure') {
    for(let i=0;i<3;i++) {
     await page.locator('.captcha-cells button:enabled').first().waitFor();
     await page.locator('.captcha-cells button').first().click();
     await page.locator('.captcha-cells button').first().click();
     if(i<2) {
      await page.waitForFunction(n=>document.querySelector('#attempt-value').textContent===String(n),2-i);
      await page.locator('.captcha-cells button:enabled').first().waitFor();
     }
    }
    await page.getByText(/尝试次数已用完/).waitFor();
   } else {
    // Exercise native keyboard controls against the real Go verifier.
    for(const i of f.targets) {await page.locator('.captcha-cells button').nth(i).focus();await page.keyboard.press('Enter');}
    await page.getByText(/验证码已通过/).waitFor();
   }
   await page.screenshot({path:path.join(output,`${name}-result.png`)});
   await context.close();
  }
  const page=await browser.newPage();
  await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'text/javascript',body:'window.Telegram={WebApp:{initData:"",ready(){},expand(){}}};'}));
  await page.goto(`${fixture.baseURL}/verify/${fixture.stale.token}`);
  await page.getByText(/无法打开此验证/).waitFor();
  assert.equal(await page.locator('#challenge-frame').isVisible(),false);
  assert.deepEqual(errors,[]);
  console.log('PASS: desktop/mobile, 320px layout, keyboard CAPTCHA, retry rotation, three failures, missing initData, skewed client clock, no page errors.');
 } finally {await browser.close();fs.writeFileSync(file+'.done','done');}
})().catch(e=>{console.error(e);process.exitCode=1;});
