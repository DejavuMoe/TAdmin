<template>
  <div class="t-steps"><slot /></div>
</template>

<style scoped>
.t-steps :deep(> ol) {
  list-style: none;
  counter-reset: step;
  padding-left: 0;
}
.t-steps :deep(> ol > li) {
  counter-increment: step;
  position: relative;
  padding-left: 44px;
  margin: 0 0 20px;
}
.t-steps :deep(> ol > li)::before {
  content: counter(step);
  position: absolute;
  left: 0;
  top: 0;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 14px;
  color: #fff;
  background: linear-gradient(135deg, var(--tadmin-sky), var(--tadmin-accent));
}
.t-steps :deep(> ol > li:not(:last-child))::after {
  content: '';
  position: absolute;
  left: 13px;
  top: 34px;
  bottom: -16px;
  width: 2px;
  background: var(--vp-c-divider);
}
</style>
