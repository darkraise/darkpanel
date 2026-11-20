import { Card, CardContent } from "darkraise-ui/components/card"
import { Skeleton } from "darkraise-ui/components/skeleton"

export function SkeletonTiles({ count, label }: { count: number; label?: string }) {
  return (
    <div
      role={label === undefined ? undefined : "status"}
      aria-busy="true"
      aria-label={label}
      className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4"
    >
      {Array.from({ length: count }, (_, i) => (
        <Card key={i}>
          <CardContent className="flex flex-col gap-3 p-4">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-8 w-16" />
            <Skeleton className="h-2 w-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
