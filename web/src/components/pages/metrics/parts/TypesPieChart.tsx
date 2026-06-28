import { Metric } from '@/lib/db/models/metric';
import { colorHash } from '@/lib/theme/color';
import { PieChart } from '@mantine/charts';

export default function TypesPieChart({ metric }: { metric: Metric }) {
  return (
    <PieChart
      data={metric.data.types.map((type) => ({
        name: type.type,
        value: type.sum,
        color: colorHash(type.type),
      }))}
      withLabels
      labelsPosition='outside'
      labelsType='value'
      withTooltip
      tooltipDataSource='segment'
      pieProps={{
        label: ({ name }) => name,
      }}
      w='100%'
      size={200}
    />
  );
}
