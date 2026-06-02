<script setup lang="ts">
import { reactive } from "vue";

const state = reactive({ count: 0, label: "hits" });
const { count, label } = state;

function bump(): void {
	state.count += 1;
}
</script>

<template>
	<button @click="bump">{{ label }}: {{ count }}</button>
</template>
