<template>
  <div class="mb-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_7rem] sm:items-start">
    <div>
      <label :for="inputId" class="input-label mb-1">
        {{ t('admin.accounts.modelMappingPercent') }}
      </label>
      <p :id="`${inputId}-hint`" class="input-hint mt-0">
        {{ t('admin.accounts.modelMappingPercentHint') }}
      </p>
    </div>
    <div>
      <div class="relative">
        <input
          :id="inputId"
          :value="modelValue ?? ''"
          type="number"
          min="0"
          max="100"
          step="1"
          inputmode="numeric"
          class="input w-full pr-8 text-right"
          :class="invalid ? 'border-red-500 focus:border-red-500 focus:ring-red-500' : ''"
          :aria-invalid="invalid"
          :aria-describedby="`${inputId}-hint`"
          @input="handleInput"
        />
        <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-sm text-gray-500 dark:text-gray-400">%</span>
      </div>
      <p v-if="invalid" class="mt-1 text-xs text-red-600 dark:text-red-400">
        {{ t('admin.accounts.modelMappingPercentInvalid') }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isValidModelMappingPercent } from './credentialsBuilder'

const props = defineProps<{
  modelValue: number | null
  inputId: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: number | null]
}>()

const { t } = useI18n()
const invalid = computed(() => !isValidModelMappingPercent(props.modelValue))

const handleInput = (event: Event) => {
  const value = (event.target as HTMLInputElement).value
  emit('update:modelValue', value === '' ? null : Number(value))
}
</script>
