// The background dancer: walks left and right along the bottom of the window,
// stops to dance, squat or point, and talks. Purely decorative; without
// JavaScript (or with prefers-reduced-motion) he just stands in the corner.
(function () {
  "use strict";

  var el = document.querySelector(".dancer");
  if (!el || !window.requestAnimationFrame || !window.matchMedia ||
      window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    return;
  }

  function part(sel) { return el.querySelector(sel); }
  var flip = part(".flip"), body = part(".body"), upper = part(".upper"), head = part(".head"),
    legL = part(".leg-l"), legR = part(".leg-r"), shinL = part(".shin-l"), shinR = part(".shin-r"),
    footL = part(".foot-l"), footR = part(".foot-r"), armL = part(".arm-l"), armR = part(".arm-r"),
    foreL = part(".fore-l"), foreR = part(".fore-r"), tie = part(".tie"),
    finger = part(".finger"), flap = part(".flap"), say = part(".dancer-say");

  // Real Trump quotes (verbatim, sometimes trimmed), grouped by what he is doing.
  var quips = {
    walk: [
      "Our army manned the air, it rammed the ramparts, it took over the airports.", // July 4th speech, 2019
      "People are flushing toilets 10 times, 15 times, as opposed to once.", // White House roundtable, 2019
      "And they say the noise causes cancer.", // on windmills, 2019
      "It's freezing and snowing in New York - we need global warming!", // tweet, 2012
      "The late, great Hannibal Lecter.", // rallies, 2024
      "They're eating the dogs. They're eating the cats.", // debate, 2024
      "I'll take electrocution every single time. I'm not getting near the shark!", // rally, 2024
      "Despite the constant negative press covfefe", // tweet, 2017
      "Over 1000 hamberders etc.", // tweet, 2019
      "Airplanes are becoming far too complex to fly.", // tweet, 2019
      "Nobody knew health care could be so complicated.", // 2017
      "Essentially, it's a large real estate deal.", // on buying Greenland, 2019
      "I'm speaking with myself, number one, because I have a very good brain.", // MSNBC, 2016
      "I think I'm much more humble than you would understand.", // 60 Minutes, 2016
      "I know words. I have the best words." // rally, 2015
    ],
    dance: [
      "We're going to win so much, you're going to be so sick and tired of winning.", // rally, 2016
      "I am the chosen one.", // 2019
      "To me, the most beautiful word in the dictionary is tariff.", // Economic Club of Chicago, 2024
      "The beauty of me is that I'm very rich.", // Good Morning America, 2011
      "I alone can fix it." // RNC, 2016
    ],
    squat: [
      "Person, woman, man, camera, TV.", // on his cognitive test, 2020
      "I'm, like, a really smart person.", // 2016
      "A very stable genius at that!", // tweet, 2018
      "Sorry losers and haters, but my I.Q. is one of the highest - and you all know it!", // tweet, 2013
      "My fingers are long and beautiful, as, it has been well documented, are various other parts of my body." // New York Post, 2011
    ],
    point: [
      "You're fired!", // The Apprentice
      "Wrong.", // debate, 2016
      "Fake news!",
      "I have concepts of a plan.", // debate, 2024
      "I know more about ISIS than the generals do, believe me.", // rally, 2015
      "Nobody builds walls better than me, believe me." // campaign launch, 2015
    ]
  };

  function pick(a) { return a[Math.floor(Math.random() * a.length)]; }
  function rot(node, a, x, y) { node.setAttribute("transform", "rotate(" + a.toFixed(1) + " " + x + " " + y + ")"); }
  function sin(v) { return Math.sin(v); }

  var width = el.offsetWidth;
  var x = Math.random() * Math.max(0, window.innerWidth - width);
  var dir = Math.random() < 0.5 ? -1 : 1;
  var mode = "walk", modeAt = 0, modeUntil = 0, sayUntil = 0, phase = 0, last = 0;
  var speed = 70; // px per second

  el.classList.add("walking");

  function talk(now, text) {
    say.textContent = text;
    say.classList.add("on");
    sayUntil = now + 1500 + 55 * text.length; // long quotes stay longer
  }

  function next(now) {
    var r = Math.random();
    modeAt = now;
    if (r < 0.35) {
      mode = "dance"; modeUntil = now + 3500 + Math.random() * 2500;
      talk(now, pick(quips.dance));
    } else if (r < 0.5) {
      mode = "squat"; modeUntil = now + 3400 + Math.random() * 1700;
      talk(now, pick(quips.squat));
    } else if (r < 0.65) {
      mode = "point"; modeUntil = now + 2200;
      talk(now, pick(quips.point));
    } else {
      mode = "walk"; modeUntil = now + 3000 + Math.random() * 5000;
      if (Math.random() < 0.5) talk(now, pick(quips.walk));
    }
  }

  // Current joint angles; each frame they ease towards the target pose, so
  // switching between walking, dancing, squatting and pointing is smooth.
  var cur = null;

  function target(t) {
    var p = phase, g;
    if (mode === "walk") {
      // A proud strut: long steps, arms swinging opposite the legs.
      g = {
        hl: -24 * sin(p), hr: 24 * sin(p),
        kl: 30 * Math.max(0, Math.cos(p)), kr: 30 * Math.max(0, -Math.cos(p)),
        fl: 0, fr: 0, sl: 18 * sin(p), sr: 18 * sin(p), el: -18, er: -18,
        bob: 4 * Math.abs(Math.cos(p)), lean: -3 + 2 * sin(2 * p), nod: 3 * sin(2 * p), hair: 10 * sin(2 * p), tie: 0
      };
    } else if (mode === "dance") {
      // The rally dance: elbows bent, fists pumping in turn, hips swaying.
      var f = t * 2 * Math.PI * 1.7;
      g = {
        hl: 6 * sin(f / 2), hr: 6 * sin(f / 2), kl: 8 + 8 * sin(f), kr: 8 - 8 * sin(f), fl: 0, fr: 0,
        sl: 28 + 14 * sin(f), sr: -28 + 14 * sin(f), el: -165 + 25 * sin(f), er: 165 + 25 * sin(f),
        bob: 4 * Math.abs(sin(f)), lean: 6 * sin(f / 2), nod: -7 * sin(f / 2), hair: 14 * sin(f), tie: 0
      };
    } else if (mode === "squat") {
      // Squats, arms out front: thighs swing forward, shins back, shoes stay flat.
      // The body drops by exactly what the folded legs lose (hip to sole is 108px),
      // so the feet stay on the ground; the long tie comes to rest on the knees.
      var d = (1 - Math.cos((t - modeAt / 1000) * 2 * Math.PI * 0.6)) / 2, a = 65 * d;
      g = {
        hl: -a, hr: -a, kl: 2 * a, kr: 2 * a, fl: -a, fr: -a,
        sl: -80 * d, sr: -80 * d, el: -10 * d, er: -10 * d,
        bob: -108 * (1 - Math.cos(a * Math.PI / 180)), lean: 16 * d, nod: -8 * d, hair: 12 * d, tie: -45 * d
      };
    } else {
      // Pointing ahead, the other fist on the hip.
      var w = sin(t * 6);
      g = {
        hl: -4, hr: 4, kl: 0, kr: 0, fl: 0, fr: 0, sl: 30, el: -70, sr: -84 + 4 * w, er: 0,
        bob: 0, lean: -4, nod: -6 + 2 * w, hair: 6 * w, tie: 0
      };
    }
    return g;
  }

  function pose(t, dt) {
    var g = target(t);
    if (!cur) cur = g;
    var k = Math.min(1, dt * 14);
    for (var key in g) cur[key] += (g[key] - cur[key]) * k;
    // Knees bend backwards: in the facing direction that is clockwise for both legs.
    rot(legL, cur.hl, 87, 192); rot(legR, cur.hr, 113, 192);
    rot(shinL, cur.kl, 87, 248); rot(shinR, cur.kr, 113, 248);
    rot(footL, cur.fl, 87, 300); rot(footR, cur.fr, 113, 300);
    rot(armL, cur.sl, 62, 112); rot(armR, cur.sr, 138, 112);
    rot(foreL, cur.el, 62, 152); rot(foreR, cur.er, 138, 152);
    rot(upper, cur.lean, 100, 200); rot(head, cur.nod, 100, 100); rot(flap, cur.hair, 148, 46);
    rot(tie, cur.tie, 100, 114);
    finger.setAttribute("opacity", mode === "point" ? "1" : "0");
    body.setAttribute("transform", "translate(0 " + (-cur.bob).toFixed(1) + ")");
  }

  function frame(now) {
    if (!el.isConnected) return; // the page was replaced (progress.js)
    // All times come from the frame timestamps, so a seeded run under a fake clock
    // (UI tests) doesn't depend on how far the clock got before the page loaded.
    if (!last) modeUntil = now + 2500;
    var dt = last ? Math.min(0.05, (now - last) / 1000) : 0;
    last = now;
    width = el.offsetWidth;
    var max = Math.max(0, window.innerWidth - width);

    if (now > modeUntil) next(now);
    if (mode === "walk") {
      x += dir * speed * dt;
      phase += dt * speed / 11;
      if (x <= 0 || x >= max) {
        x = Math.min(max, Math.max(0, x));
        dir = -dir;
      }
    }
    if (now > sayUntil) say.classList.remove("on");
    el.classList.toggle("talking", now < sayUntil && Math.floor(now / 140) % 2 === 0);

    pose(now / 1000, dt);
    flip.setAttribute("transform", dir < 0 ? "translate(200 0) scale(-1 1)" : "");
    el.style.transform = "translateX(" + x.toFixed(1) + "px)";
    requestAnimationFrame(frame);
  }

  requestAnimationFrame(frame);
})();
