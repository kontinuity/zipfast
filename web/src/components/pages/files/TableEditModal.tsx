import { FieldSettings, NAMES, useFileTableSettingsStore } from '@/lib/client/store/fileTableSettings';
import {
  closestCenter,
  DndContext,
  DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Button, Checkbox, Group, Modal, Paper, Text } from '@mantine/core';
import { IconGripVertical } from '@tabler/icons-react';
import { useShallow } from 'zustand/shallow';

function SortableTableField({ item }: { item: FieldSettings }) {
  const setVisible = useFileTableSettingsStore((state) => state.setVisible);

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: item.field,
  });

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    cursor: 'grab',
    width: '100%',
  };

  return (
    <Paper withBorder p='xs' ref={setNodeRef} style={style} {...attributes} {...listeners}>
      <Group gap='xs'>
        <IconGripVertical size='1rem' />

        <Checkbox checked={item.visible} onChange={() => setVisible(item.field, !item.visible)} />

        <Text>{NAMES[item.field]}</Text>
      </Group>
    </Paper>
  );
}

export default function TableEditModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const [fields, setIndex, reset] = useFileTableSettingsStore(
    useShallow((state) => [state.fields, state.setIndex, state.reset]),
  );

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (active.id !== over?.id) {
      const newIndex = fields.findIndex((item) => item.field === over?.id);

      setIndex(active.id as FieldSettings['field'], newIndex);
    }
  };

  return (
    <Modal opened={opened} onClose={onClose} title='Table Options' centered>
      <Text mb='md' size='sm' c='dimmed'>
        Select and drag fields below to make them appear/disappear/reorder in the file table view.
      </Text>

      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={fields.map((item) => item.field)} strategy={verticalListSortingStrategy}>
          {fields.map((item, index) => (
            <div
              key={index}
              style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}
            >
              <SortableTableField item={item} />
            </div>
          ))}
        </SortableContext>
      </DndContext>

      <Button fullWidth color='red' onClick={() => reset()} variant='light' mt='md'>
        Reset to Default
      </Button>
    </Modal>
  );
}
